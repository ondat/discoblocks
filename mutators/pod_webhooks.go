package mutators

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/pkg/namesgenerator"
	discoblocksondatiov1 "github.com/ondat/discoblocks/api/v1"
	"github.com/ondat/discoblocks/pkg/drivers"
	"github.com/ondat/discoblocks/pkg/utils"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// log is for logging in this package
var podMutatorLog = logf.Log.WithName("mutators.PodMutator")

type PodMutator struct {
	Client  client.Client
	strict  bool
	decoder *admission.Decoder
}

//+kubebuilder:webhook:path=/mutate-v1-pod,mutating=true,sideEffects=none,failurePolicy=fail,groups="",resources=pods,verbs=create,versions=v1,admissionReviewVersions=v1,name=mpod.kb.io

// Handle pod mutation
//nolint:gocyclo // It is complex we know
func (a *PodMutator) Handle(ctx context.Context, req admission.Request) admission.Response {
	logger := podMutatorLog.WithValues("req_name", req.Name, "req_namespace", req.Namespace)

	logger.Info("Handling...")
	defer logger.Info("Handled")

	pod := corev1.Pod{}
	if err := a.decoder.Decode(req, &pod); err != nil {
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("unable to decode request: %w", err))
	}

	// In some cases decoded pod doesn't have these fields
	pod.Name = req.Name
	pod.Namespace = req.Namespace

	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	logger.Info("Fetch DiskConfigs...")

	diskConfigs := discoblocksondatiov1.DiskConfigList{}
	if err := a.Client.List(ctx, &diskConfigs, &client.ListOptions{
		Namespace: pod.Namespace,
	}); err != nil {
		return admission.Errored(http.StatusInternalServerError, fmt.Errorf("unable to fetch configs: %w", err))
	}

	if len(diskConfigs.Items) == 0 {
		return admission.Allowed("DiskConfig not found in namespace: " + pod.Namespace)
	}

	errorMode := func(code int32, reason string, err error) admission.Response {
		if a.strict {
			return admission.Errored(code, err)
		}

		return admission.Allowed(reason)
	}

	nameGeneratorOnce := sync.Once{}

	volumes := map[string]string{}
	for i := range diskConfigs.Items {
		if diskConfigs.Items[i].DeletionTimestamp != nil {
			continue
		}

		config := diskConfigs.Items[i]

		if !utils.IsContainsAll(pod.Labels, config.Spec.PodSelector) {
			continue
		}

		logger := logger.WithValues("name", config.Name, "sc_name", config.Spec.StorageClassName)

		if pod.Spec.HostPID && !config.Spec.Policy.Pause {
			msg := "Autoscaling and Pod.Spec.HostPID are not supported together"
			logger.Info(msg)
			return errorMode(http.StatusBadRequest, msg, errors.New(strings.ToLower(msg)))
		}

		if pod.Name == "" {
			var nameGenErr error
			nameGeneratorOnce.Do(func() {
				if len(pod.OwnerReferences) == 0 {
					pod.Name = fmt.Sprintf("%s-%d", namesgenerator.GetRandomName(0), time.Now().UnixNano())
				} else {
					nameParts := []string{pod.OwnerReferences[0].Name}
					for _, r := range pod.OwnerReferences {
						nameParts = append(nameParts, r.Name)
					}
					nameParts = append(nameParts, fmt.Sprintf("%d", time.Now().UnixNano()))

					pod.Name, nameGenErr = utils.RenderResourceName(false, nameParts...)
				}

				logger = podMutatorLog.WithValues("name", pod.Name, "namespace", pod.Namespace)
			})
			if nameGenErr != nil {
				return admission.Errored(http.StatusInternalServerError, fmt.Errorf("unable to render resource name: %w", nameGenErr))
			}
		}

		if pod.Labels == nil {
			pod.Labels = map[string]string{}
		}
		pod.Labels[utils.RenderDiskConfigLabel(config.Name)] = config.Name
		pod.Labels["discoblocks-metrics"] = pod.Name

		logger.Info("Fetch StorageClass...")

		sc := storagev1.StorageClass{}
		if err := a.Client.Get(ctx, types.NamespacedName{Name: config.Spec.StorageClassName}, &sc); err != nil {
			if apierrors.IsNotFound(err) {
				logger.Info("StorageClass not found", "name", config.Spec.StorageClassName)
				return errorMode(http.StatusNotFound, "StorageClass not found: "+config.Spec.StorageClassName, err)
			}
			logger.Info("Unable to fetch StorageClass", "error", err.Error())
			return admission.Errored(http.StatusInternalServerError, fmt.Errorf("unable to fetch StorageClass: %w", err))
		}
		logger = logger.WithValues("provisioner", sc.Provisioner)

		driver := drivers.GetDriver(sc.Provisioner)
		if driver == nil {
			logger.Info("Driver not found")
			return errorMode(http.StatusInternalServerError, "Driver not found: "+sc.Provisioner, fmt.Errorf("driver not found: %s", sc.Provisioner))
		}

		logger.Info("Attach volume to workload...")

		prefix := utils.GetNamePrefix(config.Spec.AvailabilityMode, config.CreationTimestamp.String())

		var pvc *corev1.PersistentVolumeClaim
		pvc, err := utils.NewPVC(&config, prefix, driver)
		if err != nil {
			return errorMode(http.StatusInternalServerError, err.Error(), err)
		}
		logger = logger.WithValues("pvc_name", pvc.Name)

		pvcNamesWithMount := map[string]string{
			pvc.Name: utils.RenderMountPoint(config.Spec.MountPointPattern, pvc.Name, 0),
		}

		logger.Info("Create PVC...")
		if err = a.Client.Create(ctx, pvc); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				logger.Info("Failed to create PVC", "error", err.Error())
				return admission.Errored(http.StatusInternalServerError, fmt.Errorf("unable to create PVC: %w", err))
			}

			logger.Info("PVC already exists")

			if config.Spec.AvailabilityMode != discoblocksondatiov1.ReadWriteOnce {
				label, err := labels.NewRequirement("discoblocks-parent", selection.Equals, []string{pvc.Name})
				if err != nil {
					logger.Error(err, "Unable to parse PVC label selector")
					return admission.Errored(http.StatusInternalServerError, fmt.Errorf("unable to parse PVC label selectors: %w", err))
				}
				pvcSelector := labels.NewSelector().Add(*label)

				logger.Info("Fetch PVCs...")

				pvcs := corev1.PersistentVolumeClaimList{}
				if err = a.Client.List(ctx, &pvcs, &client.ListOptions{
					Namespace:     config.Namespace,
					LabelSelector: pvcSelector,
				}); err != nil {
					logger.Error(err, "Unable to fetch PVCs")
					return admission.Errored(http.StatusInternalServerError, fmt.Errorf("unable to fetch PVCs: %w", err))
				}

				sort.Slice(pvcs.Items, func(i, j int) bool {
					return pvcs.Items[i].CreationTimestamp.UnixNano() < pvcs.Items[j].CreationTimestamp.UnixNano()
				})

				for i := range pvcs.Items {
					if pvcs.Items[i].DeletionTimestamp != nil || !controllerutil.ContainsFinalizer(&pvcs.Items[i], utils.RenderFinalizer(config.Name)) {
						continue
					}

					if _, ok := pvcs.Items[i].Labels["discoblocks-index"]; !ok {
						err = errors.New("volume index not found")
						logger.Error(err, "Volume index not found")
						return admission.Errored(http.StatusInternalServerError, err)
					}

					index, err := strconv.Atoi(pvcs.Items[i].Labels["discoblocks-index"])
					if err != nil {
						logger.Error(err, "Unable to convert index")
						return admission.Errored(http.StatusInternalServerError, fmt.Errorf("unable to convert index: %w", err))
					}

					pvcNamesWithMount[pvcs.Items[i].Name] = utils.RenderMountPoint(config.Spec.MountPointPattern, pvcs.Items[i].Name, index)

					logger.Info("Volume found", "pvc_name", pvcs.Items[i].Name, "mountpoint", pvcNamesWithMount[pvcs.Items[i].Name])
				}
			}
		}

		for pvcName, mountpoint := range pvcNamesWithMount {
			for name, mp := range volumes {
				if mp == mountpoint {
					logger.Info("Mount point already added", "exists", name, "actual", pvcName, "mountpoint", sc.Provisioner)
					return errorMode(http.StatusInternalServerError, "Unable to init a PVC", fmt.Errorf("mount point already added: %s:/%s", pvcName, name))
				}
			}

			volumes[pvcName] = mountpoint

			pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
				Name: pvcName,
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: pvcName,
					},
				},
			})
		}
	}

	if len(volumes) == 0 {
		return admission.Allowed("No sidecar injection")
	}

	pod.Spec.SchedulerName = "discoblocks-scheduler"

	logger.Info("Attach sidecars...")

	metricsSideCar, err := utils.RenderMetricsSidecar()
	if err != nil {
		logger.Error(err, "Metrics sidecar template invalid")
		return admission.Allowed("Metrics sidecar template invalid")
	}
	pod.Spec.Containers = append(pod.Spec.Containers, *metricsSideCar)

	logger.Info("Attach volume mounts...")

	for i := range pod.Spec.Containers {
		for name, mp := range volumes {
			pod.Spec.Containers[i].VolumeMounts = append(pod.Spec.Containers[i].VolumeMounts, corev1.VolumeMount{
				Name:      name,
				MountPath: mp,
			})
		}
	}

	marshaledPod, err := json.Marshal(pod)
	if err != nil {
		logger.Error(err, "Unable to marshal pod")
		return admission.Errored(http.StatusInternalServerError, fmt.Errorf("unable to marshal pod: %w", err))
	}

	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledPod)
}

// InjectDecoder sets decoder
func (a *PodMutator) InjectDecoder(d *admission.Decoder) error {
	a.decoder = d
	return nil
}

// NewPodMutator creates a new pod mutator
func NewPodMutator(kubeClient client.Client, strict bool) *PodMutator {
	return &PodMutator{
		Client: kubeClient,
		strict: strict,
	}
}
