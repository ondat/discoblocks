package mutators

import (
	"context"
	"encoding/json"
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
var podMutatorLog = logf.Log.WithName("pod-mutator")

type PodMutator struct {
	Client  client.Client
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

	volumes := map[string]string{}

	for i := range diskConfigs.Items {
		config := diskConfigs.Items[i]

		if !utils.IsContainsAll(pod.Labels, config.Spec.PodSelector) {
			continue
		}

		//nolint:govet // logger is ok to shadowing
		logger := logger.WithValues("name", config.Name, "sc_name", config.Spec.StorageClassName)
		logger.Info("Attach volume to workload...")

		capacity, err := resource.ParseQuantity(config.Spec.Capacity)
		if err != nil {
			logger.Error(err, "Capacity is invalid")
			return admission.Allowed("Capacity is invalid:" + config.Spec.Capacity)
		}

		logger.Info("Fetch StorageClass...")

		sc := storagev1.StorageClass{}
		if err = a.Client.Get(ctx, types.NamespacedName{Name: config.Spec.StorageClassName}, &sc); err != nil {
			if apierrors.IsNotFound(err) {
				logger.Info("StorageClass not found")
				return admission.Allowed("StorageClass not found: " + config.Spec.StorageClassName)
			}
			logger.Info("Unable to fetch StorageClass", "error", err.Error())
			return admission.Errored(http.StatusInternalServerError, fmt.Errorf("unable to fetch StorageClass: %w", err))
		}
		logger = logger.WithValues("provisioner", sc.Provisioner)

		driver := drivers.GetDriver(sc.Provisioner)
		if driver == nil {
			logger.Info("Driver not found")
			return admission.Allowed("Driver not found: " + sc.Provisioner)
		}

		// TODO time.Now() blocks PVC reuse
		pvcName, err := utils.RenderPVCName(time.Now().String(), config.CreationTimestamp.String(), config.Namespace+config.Name)
		if err != nil {
			logger.Error(err, "Unable to calculate hash")
			return admission.Allowed("unable to calculate hash")
		}
		logger = logger.WithValues("pvc_name", pvcName)

		pvc, err := driver.GetPVCStub(pvcName, config.Namespace, config.Spec.StorageClassName)
		if err != nil {
			logger.Error(err, "Unable to init a PVC", "provisioner", sc.Provisioner)
			return admission.Allowed("Unable to init a PVC")
		}

		pvc.Finalizers = []string{utils.RenderFinalizer(config.Name)}
		pvc.Labels = map[string]string{
			"discoblocks": config.Name,
		}
		pvc.Spec.Resources.Requests[corev1.ResourceStorage] = capacity

		logger.Info("Create PVCs...")
		if err = a.Client.Create(ctx, pvc); err != nil {
			logger.Info("Failed to create PVC", "error", err.Error())
			return admission.Errored(http.StatusInternalServerError, fmt.Errorf("unable to create PVC: %w", err))
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

	if len(volumes) != 0 {
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

		pod.Spec.SchedulerName = "discoblocks-scheduler"

		marshaledPod, err := json.Marshal(pod)
		if err != nil {
			logger.Error(err, "Unable to marshal pod")
			return admission.Errored(http.StatusInternalServerError, fmt.Errorf("unable to marshal pod: %w", err))
		}

		return admission.PatchResponseFromRaw(req.Object.Raw, marshaledPod)
	}

	return admission.Allowed("No sidecar injection")
}

// InjectDecoder sets decoder
func (a *PodMutator) InjectDecoder(d *admission.Decoder) error {
	a.decoder = d
	return nil
}
