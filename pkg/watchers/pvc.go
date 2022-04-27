package watchers

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	tcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

// PersistentVolumeClaimWatcher watches StorageOS related PVCs
type PersistentVolumeClaimWatcher struct {
	name    string
	client  tcorev1.PersistentVolumeClaimInterface
	listOpt metav1.ListOptions
	log     logr.Logger
}

// setup configures PVC watcher with init values
func (w *PersistentVolumeClaimWatcher) Setup(ctx context.Context, fromNow bool, labelSelector string) error {
	w.listOpt = metav1.ListOptions{}

	if labelSelector != "" {
		w.listOpt.LabelSelector = labelSelector
	}

	var pvcs *corev1.PersistentVolumeClaimList
	if fromNow {
		var cancel func()
		ctx, cancel = context.WithTimeout(ctx, time.Minute)
		defer cancel()

		var err error
		if pvcs, err = w.client.List(ctx, w.listOpt); err != nil {
			return err
		}
	}

	w.listOpt.Watch = true
	if pvcs != nil {
		w.listOpt.ResourceVersion = pvcs.ResourceVersion
	}

	return nil
}

// Start watching PVCs
func (w *PersistentVolumeClaimWatcher) Start(ctx context.Context, watchConsumer WatchConsumer) <-chan error {
	return start(ctx, w.name, watchChange(w.client, w.listOpt, watchConsumer))
}

// NewPersistentVolumeClaimWatcher consructs a new PVC watcher
func NewPersistentVolumeClaimWatcher(client tcorev1.PersistentVolumeClaimInterface, name string) *PersistentVolumeClaimWatcher {
	return &PersistentVolumeClaimWatcher{
		name:   name,
		client: client,
		log:    ctrl.Log.WithName(name + " PVC watcher"),
	}
}
