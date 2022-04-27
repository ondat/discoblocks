package schedulers

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	discoblocksondatiov1 "github.com/ondat/discoblocks/api/v1"
	"github.com/ondat/discoblocks/pkg/utils"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	framework "k8s.io/kubernetes/pkg/scheduler/framework"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type podFilter struct {
	client.Client
	logger logr.Logger
}

// Name returns the name of plugin
func (s *podFilter) Name() string {
	return "PodFilter"
}

// Filter does the filtering
func (s *podFilter) Filter(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
	logger := s.logger.WithName(pod.Name).WithValues("namespace", pod.Name, "node", nodeInfo.Node().Name)

	logger.Info("Scheduling...")
	defer logger.Info("Scheduled")

	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	diskConfigs := discoblocksondatiov1.DiskConfigList{}
	if err := s.Client.List(ctx, &diskConfigs, &client.ListOptions{
		Namespace: pod.Namespace,
	}); err != nil {
		logger.Error(err, "Failed to fetch DiskConfigs")
		framework.NewStatus(framework.Success, err.Error())
	}

	nodeSelector := map[string]string{}

	for i := range diskConfigs.Items {
		config := diskConfigs.Items[i]

		if !utils.IsContainsAll(pod.Labels, config.Spec.PodSelector) {
			continue
		}

		if config.Spec.NodeSelector != nil {
			for key, value := range config.Spec.NodeSelector.MatchLabels {
				if old, ok := nodeSelector[key]; ok && old != value {
					logger.Info("Label with mutiple value is not supported", "key", key, "value_1", old, "value_2", value, "config", config.Name)
					framework.NewStatus(framework.Success, fmt.Sprintf("label with mutiple value is not supported %s=%s | %s in %s", key, old, value, config.Name))
				}

				nodeSelector[key] = value
			}
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
