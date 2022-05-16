package schedulers

import (
	"os"

	"github.com/go-logr/logr"
	"golang.org/x/net/context"
	scheduler "k8s.io/kubernetes/cmd/kube-scheduler/app"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// log is for logging in this package
var schedulerLog = logf.Log.WithName("Scheduler")

// Scheduler HTTP service for schedulers
type Scheduler struct {
	client.Client
	strict bool
	logger logr.Logger
}

// Start starts request handling
func (s *Scheduler) Start(ctx context.Context) <-chan error {
	s.logger.Info("Plugin start...")

	errChan := make(chan error)

	go func() {
		defer s.logger.Info("Plugin stop")
		defer close(errChan)

		podFilterPlugin := podFilter{
			Client: s.Client,
			strict: s.strict,
			logger: s.logger.WithName("Pod"),
		}

		command := scheduler.NewSchedulerCommand(scheduler.WithPlugin(podFilterPlugin.Name(), podFilterPlugin.Factory))
		command.SetOut(&logWriter{s.logger})
		command.SetErr(os.Stderr)
		command.SetArgs([]string{"--config=/etc/kubernetes/discoblocks-scheduler/scheduler-config.yaml"})
		if err := command.ExecuteContext(ctx); err != nil {
			s.logger.Error(err, "Scheduler plugin crashed")
			errChan <- err
			return
		}
	}()

	return errChan
}

// NewScheduler creates a new scheduler
func NewScheduler(kubeClient client.Client, strict bool) *Scheduler {
	return &Scheduler{
		Client: kubeClient,
		strict: strict,
		logger: schedulerLog,
	}
}

type logWriter struct {
	logr.Logger
}

// Write turns input to log message
func (w *logWriter) Write(p []byte) (int, error) {
	w.Logger.Info(string(p))
	return len(p), nil
}
