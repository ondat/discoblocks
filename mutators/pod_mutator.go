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

var _ admission.Handler = &PodMutator{}

type PodMutator struct {
	Client  client.Client
	strict  bool
	decoder *admission.Decoder
}

//+kubebuilder:webhook:path=/mutate-v1-pod,mutating=true,sideEffects=NoneOnDryRun,failurePolicy=fail,groups="",resources=pods,verbs=create,versions=v1,admissionReviewVersions=v1,name=mpod.kb.io

// Handle pod mutation
//nolint:gocyclo // It is complex we know
func (a *PodMutator) Handle(ctx context.Context, req admission.Request) admission.Response {
	logger := podMutatorLog.WithValues("req_name", req.Name, "namespace", req.Namespace)

	logger.Info("Handling...")
	defer logger.Info("Handled")

	pod := corev1.Pod{}
	if err := a.decoder.DecodeRaw(req.Object, &pod); err != nil {
		logger.Info("Unable to decode request", "error", err.Error())
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
		logger.Info("Unable to fetch DiskConfigs", "error", err.Error())
		return admission.Errored(http.StatusInternalServerError, fmt.Errorf("unable to fetch DiskConfigs: %w", err))
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

	nodeName := utils.GetTargetNodeByAffinity(pod.Spec.Affinity)

	logger = logger.WithValues("node_name", nodeName)

	diskConfigTypes := map[discoblocksondatiov1.AvailabilityMode]bool{}

	volumes := map[string]string{}
	for i := range diskConfigs.Items {
		if diskConfigs.Items[i].DeletionTimestamp != nil {
			continue
		} else if !utils.IsContainsAll(pod.Labels, diskConfigs.Items[i].Spec.PodSelector) {
			continue
		}

		config := diskConfigs.Items[i]

		logger := logger.WithValues("dc_name", config.Name, "sc_name", config.Spec.StorageClassName)

		if config.Spec.AvailabilityMode == discoblocksondatiov1.ReadWriteDaemon {
			if nodeName == "" {
				msg := "Node name not found for ReadWriteDaemons at node affinities"
				logger.Info(msg)
				return errorMode(http.StatusBadRequest, msg, errors.New(strings.ToLower(msg)))
			}

			if diskConfigTypes[discoblocksondatiov1.ReadWriteSame] {
				msg := "ReadWriteDaemon and ReadWriteSame are not supported together"
				logger.Info(msg)
				return errorMode(http.StatusBadRequest, msg, errors.New(strings.ToLower(msg)))
			}
			diskConfigTypes[config.Spec.AvailabilityMode] = true

			if !utils.IsOwnedByDaemonSet(&pod) {
				msg := "ReadWriteDaemon supports only apps/v1.DaemonSets"
				logger.Info(msg)
				return errorMode(http.StatusBadRequest, msg, errors.New(strings.ToLower(msg)))
			}
		}

		if pod.Name == "" {
			if len(pod.OwnerReferences) == 0 {
				pod.Name = fmt.Sprintf("%s-%d", namesgenerator.GetRandomName(0), time.Now().UnixNano())
			} else {
				nameParts := []string{pod.OwnerReferences[0].Name, namesgenerator.GetRandomName(0)}
				for _, r := range pod.OwnerReferences {
					nameParts = append(nameParts, r.Name)
				}

				var err error
				pod.Name, err = utils.RenderResourceName(false, nameParts...)
				if err != nil {
					msg := fmt.Sprintf("Unable to render resource name: %s", err.Error())
					logger.Info(msg)
					return errorMode(http.StatusInternalServerError, msg, fmt.Errorf("unable to render resource name: %w", err))
				}
			}
		}

		if pod.Labels == nil {
			pod.Labels = map[string]string{}
		}
		pod.Labels[utils.RenderUniqueLabel(config.Name)] = config.Name
		pod.Labels["discoblocks-metrics"] = pod.Name

		logger.Info("Fetch StorageClass...")

		sc := storagev1.StorageClass{}
		if err := a.Client.Get(ctx, types.NamespacedName{Name: config.Spec.StorageClassName}, &sc); err != nil {
			if apierrors.IsNotFound(err) {
				logger.Info("StorageClass not found", "name", config.Spec.StorageClassName)
				return admission.Errored(http.StatusNotFound, err)
			}
			logger.Info("Unable to fetch StorageClass", "error", err.Error())
			return admission.Errored(http.StatusInternalServerError, fmt.Errorf("unable to fetch StorageClass: %w", err))
		}
		logger = logger.WithValues("provisioner", sc.Provisioner)

		driver := drivers.GetDriver(sc.Provisioner)
		if driver == nil {
			msg := fmt.Sprintf("Driver not found: %s", sc.Provisioner)
			logger.Info(msg)
			return errorMode(http.StatusInternalServerError, msg, fmt.Errorf("driver not found: %s", sc.Provisioner))
		}

		logger.Info("Attach volume to workload...")

		prefix := utils.GetNamePrefix(config.Spec.AvailabilityMode, string(config.UID), nodeName)

		var pvc *corev1.PersistentVolumeClaim
		pvc, err := utils.NewPVC(&config, prefix, driver)
		if err != nil {
			msg := fmt.Sprintf("Failed to get NewPVC: %s", err.Error())
			logger.Error(err, msg)
			return errorMode(http.StatusInternalServerError, msg, fmt.Errorf("failed to get NewPVC: %s", err.Error()))
		}
		logger = logger.WithValues("pvc_name", pvc.Name)

		pvcNamesWithMount := map[string]string{
			pvc.Name: utils.RenderMountPoint(config.Spec.MountPointPattern, pvc.Name, 0),
		}

		if req.DryRun == nil || !*req.DryRun {
			if nodeName != "" {
				logger.Info("Fetch Node...")

				node := &corev1.Node{}
				if err := a.Client.Get(ctx, types.NamespacedName{Name: nodeName}, node); err != nil {
					return admission.Errored(http.StatusInternalServerError, err)
				}

				scAllowedTopology, err := driver.GetStorageClassAllowedTopology(node)
				if err != nil {
					msg := fmt.Sprintf("Failed to get GetStorageClassAllowedTopology: %s", err.Error())
					logger.Info(msg)
					return errorMode(http.StatusInternalServerError, msg, fmt.Errorf("failed to get GetStorageClassAllowedTopology: %s", err.Error()))
				}

				if len(scAllowedTopology) != 0 {
					topologySC, err := utils.NewStorageClass(&sc, scAllowedTopology)
					if err != nil {
						msg := fmt.Sprintf("Failed to get NewStorageClass: %s", err.Error())
						logger.Error(err, msg)
						return errorMode(http.StatusInternalServerError, msg, fmt.Errorf("failed to get NewStorageClass: %s", err.Error()))
					}

					logger.Info("Create StorageClass...")

					if err = a.Client.Create(ctx, topologySC); err != nil {
						if !apierrors.IsAlreadyExists(err) {
							return admission.Errored(http.StatusInternalServerError, err)
						}
					}

					pvc.Spec.StorageClassName = &topologySC.Name
				}
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
						msg := fmt.Sprintf("Unable to parse PVC label selectors: discoblocks-parent=%s", pvc.Name)
						logger.Error(err, msg)
						return errorMode(http.StatusInternalServerError, msg, fmt.Errorf("unable to parse PVC label selectors: %w", err))
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
							return errorMode(http.StatusInternalServerError, "Volume index not found", err)
						}

						index, err := strconv.Atoi(pvcs.Items[i].Labels["discoblocks-index"])
						if err != nil {
							msg := fmt.Sprintf("Unable to convert index: %s", pvcs.Items[i].Labels["discoblocks-index"])
							logger.Error(err, msg)
							return errorMode(http.StatusInternalServerError, msg, fmt.Errorf("unable to convert index: %w", err))
						}

						pvcNamesWithMount[pvcs.Items[i].Name] = utils.RenderMountPoint(config.Spec.MountPointPattern, pvcs.Items[i].Name, index)

						logger.Info("Volume found", "pvc_name", pvcs.Items[i].Name, "mountpoint", pvcNamesWithMount[pvcs.Items[i].Name])
					}
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

	logger.Info("Attach sidecar...")

	metricsSideCar, err := utils.RenderMetricsSidecar(pod.Spec.HostPID)
	if err != nil {
		logger.Error(err, "Metrics sidecar template invalid")
		return admission.Allowed("Metrics sidecar template invalid")
	}
	pod.Spec.Containers = append(pod.Spec.Containers, *metricsSideCar)

	for _, vm := range metricsSideCar.VolumeMounts {
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
			Name: vm.Name,
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: vm.MountPath,
				},
			},
		})
	}

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
