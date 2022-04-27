package watchers

import (
	"context"
	"errors"
	"time"

	discoblocksondatiov1 "github.com/ondat/discoblocks/api/v1"
	"github.com/ondat/discoblocks/pkg/watchers"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// log is for logging in this package
var pvcWatcherLog = logf.Log.WithName("pvc-watcher")

// PVCWatcher watches PVCs and updates DickConfig status
type PVCWatcher struct {
	kubeClient    client.Client
	kubeClientSet kubernetes.Clientset
}

// Watch start watching
func (w PVCWatcher) Watch(ctx context.Context) <-chan error {
	watcher := watchers.NewPersistentVolumeClaimWatcher(w.kubeClientSet.CoreV1().PersistentVolumeClaims(""), "PVC change")
	if err := watcher.Setup(ctx, true, "discoblocks"); err != nil {
		pvcWatcherLog.Error(err, "Unable to set PVC watcher")
		errChan := make(chan error, 1)
		errChan <- err
		return errChan
	}

	// TODO replace to informer https://banzaicloud.com/blog/k8s-custom-scheduler/
	return watcher.Start(ctx, func(watchChan <-chan watch.Event) error {
		for {
			var event watch.Event
			var ok bool
			select {
			case <-ctx.Done():
				pvcWatcherLog.Info("Context has closed")
				return nil
			case event, ok = <-watchChan:
				if !ok {
					pvcWatcherLog.Info("Watcher has closed")
					return errors.New("watcher has closed")
				}
			}

			if event.Type != watch.Modified {
				continue
			}

			pvc, ok := event.Object.(*corev1.PersistentVolumeClaim)
			if !ok {
				pvcWatcherLog.Error(errors.New("invalid object type"), event.Object.GetObjectKind().GroupVersionKind().Kind)
				continue
			}

			logger := pvcWatcherLog.WithValues("name", pvc.Name)
			logger.Info("Update PVC phase...")

			if _, ok := pvc.Labels["discoblocks/name"]; !ok {
				pvcWatcherLog.Info("Label not found")
				continue
			}

			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)

			logger.Info("Fetching DiskConfig...")

			config := discoblocksondatiov1.DiskConfig{}
			if err := w.kubeClient.Get(ctx, types.NamespacedName{Namespace: pvc.Namespace, Name: pvc.Labels["discoblocks/name"]}, &config); err != nil {
				cancel()

				if apierrors.IsNotFound(err) {
					logger.Info("DiskConfig not found")
					continue
				}

				logger.Info("Unable to fetch DiskConfig", "error", err.Error())
				return errors.New("unable to fetch DiskConfig")
			}
			logger = logger.WithValues("dc_name", config.Name)

			if config.Status.PersistentVolumeClaims == nil {
				config.Status.PersistentVolumeClaims = map[string]map[string]corev1.PersistentVolumeClaimPhase{}
			}
			if config.Status.PersistentVolumeClaims[string(config.UID)] == nil {
				config.Status.PersistentVolumeClaims[string(config.UID)] = map[string]corev1.PersistentVolumeClaimPhase{}
			}

			config.Status.PersistentVolumeClaims[string(config.UID)][pvc.Name] = pvc.Status.Phase

			// TODO update conditions

			logger.Info("Updating DiskConfig...")

			if err := w.kubeClient.Status().Update(ctx, &config); err != nil {
				cancel()

				logger.Info("Unable to update DiskConfig status", "error", err.Error())
				return errors.New("unable to update DiskConfig status")
			}

			logger.Info("Updated")
			cancel()
		}
	})
}

// NewPersistentVolumeClaimWatcher creates a new watcher
func NewPersistentVolumeClaimWatcher(kubeClient client.Client, kubeClientSet kubernetes.Clientset) *PVCWatcher {
	return &PVCWatcher{
		kubeClient:    kubeClient,
		kubeClientSet: kubeClientSet,
	}
}
