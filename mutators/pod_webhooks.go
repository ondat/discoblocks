package mutators

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	discoblocksondatiov1 "github.com/ondat/discoblocks/api/v1"
	"github.com/ondat/discoblocks/pkg/drivers"
	"github.com/ondat/discoblocks/pkg/utils"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// log is for logging in this package
var podMutatorLog = logf.Log.WithName("PodMutator")

type PodMutator struct {
	Client  client.Client
	strict  bool
	decoder *admission.Decoder
}

//+kubebuilder:webhook:path=/mutate-v1-pod,mutating=true,sideEffects=none,failurePolicy=fail,groups="",resources=pods,verbs=create,versions=v1,admissionReviewVersions=v1,name=mpod.kb.io

// Handle pod mutation
func (a *PodMutator) Handle(ctx context.Context, req admission.Request) admission.Response {
	logger := podMutatorLog.WithValues("name", req.Name, "namespace", req.Namespace)

	logger.Info("Handling...")
	defer logger.Info("Handled")

	pod := corev1.Pod{}
	if err := a.decoder.Decode(req, &pod); err != nil {
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("unable to decode request: %w", err))
	}

	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	logger.Info("Fetch configs...")

	diskConfigs := discoblocksondatiov1.DiskConfigList{}
	if err := a.Client.List(ctx, &diskConfigs, &client.ListOptions{
		Namespace: pod.Namespace,
	}); err != nil {
		return admission.Errored(http.StatusInternalServerError, fmt.Errorf("unable to fetch configs: %w", err))
	}

	errorMode := func(code int32, reason string, err error) admission.Response {
		if a.strict {
			return admission.Errored(code, err)
		}

		return admission.Allowed(reason)
	}

	volumes := map[string]string{}
	for i := range diskConfigs.Items {
		config := diskConfigs.Items[i]

		if !utils.IsContainsAll(pod.Labels, config.Spec.PodSelector) {
			continue
		}

		if pod.Labels == nil {
			pod.Labels = map[string]string{}
		}
		pod.Labels["discoblocks/metrics"] = config.Name

		//nolint:govet // logger is ok to shadowing
		logger := logger.WithValues("name", config.Name, "sc_name", config.Spec.StorageClassName)
		logger.Info("Attach volume to workload...")

		capacity, err := resource.ParseQuantity(config.Spec.Capacity)
		if err != nil {
			logger.Error(err, "Capacity is invalid")
			return errorMode(http.StatusNotAcceptable, "Capacity is invalid:"+config.Spec.Capacity, err)
		}

		logger.Info("Fetch StorageClass...")

		sc := storagev1.StorageClass{}
		if err = a.Client.Get(ctx, types.NamespacedName{Name: config.Spec.StorageClassName}, &sc); err != nil {
			if apierrors.IsNotFound(err) {
				logger.Info("StorageClass not found")
				return errorMode(http.StatusNotFound, "StorageClass not found: "+config.Spec.StorageClassName, err)
			}
			logger.Info("Unable to fetch StorageClass", "error", err.Error())
			return admission.Errored(http.StatusInternalServerError, fmt.Errorf("unable to fetch StorageClass: %w", err))
		}
		logger = logger.WithValues("provisioner", sc.Provisioner)

		driver := drivers.GetDriver(sc.Provisioner)
		if driver == nil {
			logger.Info("Driver not found")
			return errorMode(http.StatusNotFound, "Driver not found: "+sc.Provisioner, errors.New("driver not found: "+sc.Provisioner))
		}

		preFix := config.CreationTimestamp.String()
		if config.Spec.AvailabilityMode != discoblocksondatiov1.Singleton {
			preFix = time.Now().String()
		}

		pvcName, err := utils.RenderPVCName(preFix, config.Name, config.Namespace)
		if err != nil {
			logger.Error(err, "Unable to calculate hash")
			return errorMode(http.StatusInternalServerError, "unable to calculate hash", err)
		}
		logger = logger.WithValues("pvc_name", pvcName)

		pvc, err := driver.GetPVCStub(pvcName, config.Namespace, config.Spec.StorageClassName)
		if err != nil {
			logger.Error(err, "Unable to init a PVC", "provisioner", sc.Provisioner)
			return errorMode(http.StatusInternalServerError, "Unable to init a PVC", err)
		}

		pvc.Finalizers = []string{utils.RenderFinalizer(config.Name)}
		pvc.Labels = map[string]string{
			"discoblocks": config.Name,
		}
		pvc.Spec.Resources = corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceStorage: capacity,
			},
		}
		pvc.Spec.AccessModes = config.Spec.AccessModes
		if len(pvc.Spec.AccessModes) == 0 {
			pvc.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
		}

		logger.Info("Create PVC...")
		if err = a.Client.Create(ctx, pvc); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				logger.Info("Failed to create PVC", "error", err.Error())
				return admission.Errored(http.StatusInternalServerError, fmt.Errorf("unable to create PVC: %w", err))
			}

			logger.Info("PVC already exists")
		}

		volumes[pvcName] = utils.RenderMountPoint(config.Spec.MountPointPattern, pvc.Name, 0)
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
			Name: pvcName,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: pvcName,
				},
			},
		})
	}

	if len(volumes) == 0 {
		return admission.Allowed("No sidecar injection")
	}

	pod.Spec.SchedulerName = "discoblocks-scheduler"

	logger.Info("Attach sidecar...")

	volumes["dev"] = "/host/dev"
	pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
		Name: "dev",
		VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{
				Path: "/dev",
			},
		},
	})

	sideCar, err := utils.RenderSidecar()
	if err != nil {
		logger.Error(err, "Sidecar template invalid")
		return admission.Allowed("Sidecar template invalid")
	}

	pod.Spec.Containers = append(pod.Spec.Containers, *sideCar)

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
