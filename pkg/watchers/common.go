package watchers

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	maxWatcherRecoveryPeriod = 30
	expo                     = 2
)

// WatchConsumer consumes watch events
type WatchConsumer func(<-chan watch.Event) error

// watchFunc manages object watching
type watchFunc func(context.Context) error

// start watching given resources
func start(ctx context.Context, name string, watchFunc watchFunc) <-chan error {
	errChan := make(chan error)

	go func() {
		log := ctrl.Log.WithName(name + " watcher")

		watcherRecoveryPeriod := 0
		for {
			err := watchFunc(ctx)
			if err == nil {
				log.Info("watch process ended gracefully")
				close(errChan)
				break
			}

			if watcherRecoveryPeriod >= maxWatcherRecoveryPeriod {
				log.Error(err, "watcher not able to recover from failure within allowed time limit")
				errChan <- err
				break
			}

			watcherRecoveryPeriod = (watcherRecoveryPeriod + 1) * expo
			log.Error(err, "there was an error while watching resource, sleep", "second", watcherRecoveryPeriod)
			time.Sleep(time.Second * time.Duration(watcherRecoveryPeriod))
		}
	}()

	return errChan
}

type watchClient interface {
	Watch(context.Context, metav1.ListOptions) (watch.Interface, error)
}

// watchChange return a func which does watch and calls consumer
func watchChange(watchClient watchClient, listOpt metav1.ListOptions, consume WatchConsumer) watchFunc {
	return func(ctx context.Context) error {
		watcher, err := watchClient.Watch(ctx, listOpt)
		if err != nil {
			return err
		}
		defer watcher.Stop()

		return consume(watcher.ResultChan())
	}
}
