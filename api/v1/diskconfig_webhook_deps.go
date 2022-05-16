package v1

import "sigs.k8s.io/controller-runtime/pkg/client"

var diskConfigWebhookDependencies *diskConfigWebhookDeps

type diskConfigWebhookDeps struct {
	client       client.Client
	provisioners map[string]bool
}

// InitDiskConfigWebhookDeps configures dependencies for webhook
func InitDiskConfigWebhookDeps(kubeClient client.Client, provisioners []string) {
	provisionersMap := map[string]bool{}
	for _, p := range provisioners {
		provisionersMap[p] = true
	}

	diskConfigWebhookDependencies = &diskConfigWebhookDeps{
		client:       kubeClient,
		provisioners: provisionersMap,
	}
}
