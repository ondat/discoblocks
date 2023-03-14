/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"sync"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.

	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/klog/v2"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	configdiscoblockv1 "github.com/ondat/discoblocks/api/config.discoblocks.ondat.io/v1"
	discoblocksondatiov1 "github.com/ondat/discoblocks/api/v1"
	"github.com/ondat/discoblocks/controllers"
	"github.com/ondat/discoblocks/mutators"
	"github.com/ondat/discoblocks/pkg/utils"
	"github.com/ondat/discoblocks/schedulers"
	//+kubebuilder:scaffold:imports
)

const (
	// envPodNamespace is the operator's pod namespace environment variable.
	envPodNamespace = "POD_NAMESPACE"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("Setup")
)

//+kubebuilder:rbac:groups=discoblocks.ondat.io,resources=diskconfigs,verbs=get;list;watch;create;update;delete
//+kubebuilder:rbac:groups=discoblocks.ondat.io,resources=diskconfigs/status,verbs=update
//+kubebuilder:rbac:groups=discoblocks.ondat.io,resources=diskconfigs/finalizers,verbs=update
//+kubebuilder:rbac:groups="storage.k8s.io",resources=volumeattachments,verbs=create;list;watch
//+kubebuilder:rbac:groups="storage.k8s.io",resources=storageclasses,verbs=get;update;create
//+kubebuilder:rbac:groups="storage.k8s.io",resources=storageclasses/finalizers,verbs=update
//+kubebuilder:rbac:groups="batch",resources=jobs,verbs=create;list;watch;delete
//+kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update
//+kubebuilder:rbac:groups="",resources=persistentvolumeclaims/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=persistentvolumes,verbs=get
//+kubebuilder:rbac:groups="",resources=secrets,verbs=create
//+kubebuilder:rbac:groups="",resources=pods,verbs=list;delete
//+kubebuilder:rbac:groups="events.k8s.io",resources=events,verbs=create

// indirect rbac
//+kubebuilder:rbac:groups="",resources=namespaces;services;pods;persistentvolumes;replicationcontrollers,verbs=list;watch
//+kubebuilder:rbac:groups="",resources=namespaces,verbs=get
//+kubebuilder:rbac:groups="apps",resources=replicasets;statefulsets,verbs=list;watch
//+kubebuilder:rbac:groups="policy",resources=poddisruptionbudgets,verbs=list;watch
//+kubebuilder:rbac:groups="storage.k8s.io",resources=storageclasses;csinodes;csidrivers;csistoragecapacities,verbs=list;watch

var controllerID = "49ccccaf.discoblocks.ondat.io"

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(discoblocksondatiov1.AddToScheme(scheme))
	utilruntime.Must(configdiscoblockv1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	http.DefaultClient.Timeout = time.Minute

	var configFile string
	flag.StringVar(&configFile, "config", "",
		"The controller will load its initial configuration from this file. "+
			"Omit this flag to use the default configuration values. ")

	const five = 5

	var leaderRenewSeconds uint
	flag.UintVar(&leaderRenewSeconds, "leader-renew-seconds", five, "Leader renewal frequency")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	zapLogger := zap.New(zap.UseFlagOptions(&opts))
	ctrl.SetLogger(zapLogger)
	klog.SetLogger(zapLogger)

	currentNS := ""
	if ns, ok := os.LookupEnv(envPodNamespace); ok {
		currentNS = ns
	}

	const ldm = 1.2
	const two = 2

	renewDeadline := time.Duration(leaderRenewSeconds) * time.Second
	leaseDuration := time.Duration(int(ldm*float64(leaderRenewSeconds))) * time.Second
	leaderRetryDuration := renewDeadline / two

	options := ctrl.Options{
		Scheme:                     scheme,
		LeaderElection:             true,
		LeaderElectionID:           controllerID,
		LeaderElectionNamespace:    currentNS,
		LeaderElectionResourceLock: resourcelock.LeasesResourceLock,
		RenewDeadline:              &renewDeadline,
		LeaseDuration:              &leaseDuration,
		RetryPeriod:                &leaderRetryDuration,
	}

	operatorConfig := configdiscoblockv1.OperatorConfig{}

	if configFile != "" {
		var err error
		cfg := ctrl.ConfigFile()
		options, err = options.AndFrom(cfg.AtPath(configFile).OfKind(&operatorConfig))
		if err != nil {
			setupLog.Error(err, "unable to load the config file")
			os.Exit(1)
		}
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), options)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	eventService := utils.NewEventService(controllerID, mgr.GetClient())

	if err = (&controllers.JobReconciler{
		EventService: eventService,
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Job")
		os.Exit(1)
	}

	nodeReconciler := &controllers.NodeReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
	if err = nodeReconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Node")
		os.Exit(1)
	}

	if err = (&controllers.DiskConfigReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "DiskConfig")
		os.Exit(1)
	}

	if _, err = (&controllers.PVCReconciler{
		OperationImage: operatorConfig.JobContainerImage,
		EventService:   eventService,
		NodeCache:      nodeReconciler,
		InProgress:     sync.Map{},
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "PVC")
		os.Exit(1)
	}

	discoblocksondatiov1.InitDiskConfigWebhookDeps(mgr.GetClient(), operatorConfig.SupportedCsiDrivers)

	if err = (&discoblocksondatiov1.DiskConfig{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create validator", "validator", "DiskConfig")
		os.Exit(1)
	}

	//+kubebuilder:scaffold:builder

	podMutator := mutators.NewPodMutator(mgr.GetClient(), operatorConfig.MutatorStrictMode, operatorConfig.ProxyContainerImage)
	mgr.GetWebhookServer().Register("/mutate-v1-pod", &webhook.Admission{Handler: podMutator})

	if err = mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}

	if err = mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	scheduler := schedulers.NewScheduler(mgr.GetClient(), operatorConfig.SchedulerStrictMode)
	schedulerErrChan := scheduler.Start(context.Background())
	go func() {
		setupLog.Error(<-schedulerErrChan, "there was an error in scheduler")
		os.Exit(1)
	}()

	setupLog.Info("Start manager")
	if err = mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
