package schedulers

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	discoblocksondatiov1 "github.com/ondat/discoblocks/api/v1"
	"github.com/ondat/discoblocks/pkg/drivers"
	"github.com/ondat/discoblocks/pkg/utils"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	framework "k8s.io/kubernetes/pkg/scheduler/framework"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type podFilter struct {
	client.Client
	strict bool
	logger logr.Logger
}

// Name returns the name of plugin
func (s *podFilter) Name() string {
	return "PodFilter"
}

// Filter does the filtering
func (s *podFilter) Filter(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
	logger := s.logger.WithValues("pod", pod.Name, "namespace", pod.Namespace, "node", nodeInfo.Node().Name)

	errorStatus := framework.Success
	if s.strict {
		errorStatus = framework.Error
	}

	logger.Info("Scheduling...")
	defer logger.Info("Scheduled")

	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	logger.Info("Fetch DiskConfigs...")

	diskConfigs := discoblocksondatiov1.DiskConfigList{}
	if err := s.Client.List(ctx, &diskConfigs, &client.ListOptions{
		Namespace: pod.Namespace,
	}); err != nil {
		logger.Error(err, "Failed to fetch DiskConfigs")
		return framework.NewStatus(errorStatus, err.Error())
	}

	storageClasses := map[string]bool{}
	nodeSelector := map[string]string{}
	for i := range diskConfigs.Items {
		config := diskConfigs.Items[i]

		if config.DeletionTimestamp != nil || !utils.IsContainsAll(pod.Labels, config.Spec.PodSelector) {
			continue
		}

		storageClasses[config.Spec.StorageClassName] = true

		if config.Spec.NodeSelector != nil {
			for key, value := range config.Spec.NodeSelector.MatchLabels {
				if old, ok := nodeSelector[key]; ok && old != value {
					logger.Info("Label with mutiple value is not supported", "key", key, "value_1", old, "value_2", value, "config", config.Name)
					framework.NewStatus(errorStatus, fmt.Sprintf("label with mutiple value is not supported %s=%s | %s in %s", key, old, value, config.Name))
				}

				nodeSelector[key] = value
			}
		}
	}

	if len(storageClasses) == 0 {
		logger.Info("DiskConfig not found")
		return framework.NewStatus(framework.Success, "DiskConfig not found")
	}

	for scName := range storageClasses {
		logger := logger.WithValues("sc_name", scName)

		logger.Info("Fetch StorageClass...")

		sc := storagev1.StorageClass{}
		if err := s.Client.Get(ctx, types.NamespacedName{Name: scName}, &sc); err != nil {
			if apierrors.IsNotFound(err) {
				logger.Info("Failed to fetch StorageClass", "error", err.Error())
				return framework.NewStatus(errorStatus, err.Error())
			}

			logger.Error(err, "Failed to fetch StorageClass")
			return framework.NewStatus(errorStatus, err.Error())
		}

		logger = logger.WithValues("provisioner", sc.Provisioner)

		driver := drivers.GetDriver(sc.Provisioner)
		if driver == nil {
			logger.Info("Driver not found")
			return framework.NewStatus(errorStatus, "driver not found: "+sc.Provisioner)
		}

		namespace, podLabels, err := driver.GetCSIDriverDetails()
		if err != nil {
			logger.Error(err, "Failed to call driver", "method", "GetCSIDriverDetails")
			return framework.NewStatus(errorStatus, "failed to call driver: "+sc.Provisioner)
		}

		found := false
		for i := range nodeInfo.Pods {
			if found = nodeInfo.Pods[i].Pod.Namespace == namespace && utils.IsContainsAll(nodeInfo.Pods[i].Pod.Labels, podLabels); found {
				break
			}
		}

		if !found {
			logger.Info("CSI Driver not found")
			return framework.NewStatus(framework.Unschedulable, "CSI Driver not found for: "+sc.Provisioner)
		}
	}

	if len(nodeSelector) == 0 {
		logger.Info("Node fits to pod")
		return framework.NewStatus(framework.Success, "Node fits to pod")
	}

	if !utils.IsContainsAll(nodeInfo.Node().Labels, nodeSelector) {
		logger.Info("Node doesn't fit to pod")
		return framework.NewStatus(framework.Unschedulable, "Node doesn't fit to pod")
	}

	logger.Info("Node fits to pod by labels")
	return framework.NewStatus(framework.Success, "Node fits to pod by labels")
}

// Factory framework compatible factory
func (s *podFilter) Factory(configuration runtime.Object, f framework.Handle) (framework.Plugin, error) {
	return s, nil
}
