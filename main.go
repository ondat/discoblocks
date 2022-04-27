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
	"os"
	"strings"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/klog/v2"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	discoblocksondatiov1 "github.com/ondat/discoblocks/api/v1"
	"github.com/ondat/discoblocks/controllers"
	"github.com/ondat/discoblocks/mutators"
	"github.com/ondat/discoblocks/schedulers"
	"github.com/ondat/discoblocks/watchers"

	//+kubebuilder:scaffold:imports

	_ "github.com/ondat/discoblocks/pkg/drivers/aws/ebs"
)

const (
	webhookport = 9443
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

//+kubebuilder:rbac:groups=discoblocks.ondat.io,resources=diskconfigs,verbs=get;list;watch;create;update;delete
//+kubebuilder:rbac:groups=discoblocks.ondat.io,resources=diskconfigs/status,verbs=update
//+kubebuilder:rbac:groups=discoblocks.ondat.io,resources=diskconfigs/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update
//+kubebuilder:rbac:groups="",resources=persistentvolumeclaims/finalizers,verbs=update
//+kubebuilder:rbac:groups="storage.k8s.io",resources=storageclasses,verbs=get;update
//+kubebuilder:rbac:groups="storage.k8s.io",resources=storageclasses/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch

// indirect rbac
//+kubebuilder:rbac:groups="",resources=namespaces;services;pods;persistentvolumes;replicationcontrollers,verbs=list;watch
//+kubebuilder:rbac:groups="apps",resources=replicasets;statefulsets,verbs=list;watch
//+kubebuilder:rbac:groups="policy",resources=poddisruptionbudgets,verbs=list;watch
//+kubebuilder:rbac:groups="storage.k8s.io",resources=storageclasses;csinodes;csidrivers;csistoragecapacities,verbs=list;watch

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(discoblocksondatiov1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	zapLogger := zap.New(zap.UseFlagOptions(&opts))
	ctrl.SetLogger(zapLogger)
	klog.SetLogger(zapLogger)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   webhookport,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "49ccccaf.discoblocks.ondat.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&controllers.DiskConfigReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "DiskConfig")
		os.Exit(1)
	}

	provisioners := strings.Split(strings.ReplaceAll(os.Getenv("SUPPORTED_CSI_DRIVERS"), " ", ""), ",")

	discoblocksondatiov1.InitDiskConfigWebhookDeps(mgr.GetClient(), provisioners)

	if err = (&discoblocksondatiov1.DiskConfig{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "DiskConfig")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	mgr.GetWebhookServer().Register("/mutate-v1-pod", &webhook.Admission{Handler: &mutators.PodMutator{Client: mgr.GetClient()}})

	defaultRestConfig := ctrl.GetConfigOrDie()
	defaultKubeClient := kubernetes.NewForConfigOrDie(defaultRestConfig)

	ctx, cancel := context.WithCancel(context.Background())

	// TODO leader election is missing
	watcherErrChan := watchers.NewPersistentVolumeClaimWatcher(mgr.GetClient(), *defaultKubeClient).Watch(ctx)
	go func() {
		setupLog.Error(<-watcherErrChan, "there was an error during watch")
		cancel()
		os.Exit(1)
	}()

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		cancel()
		os.Exit(1)
	}
	// TODO proper ready check would be nice
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		cancel()
		os.Exit(1)
	}

	// TODO leader election is missing
	scheduler := schedulers.NewScheduler(mgr.GetClient())
	schedulerErrChan := scheduler.Start(ctx)
	go func() {
		setupLog.Error(<-schedulerErrChan, "there was an error in scheduler")
		cancel()
		os.Exit(1)
	}()

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		cancel()
		os.Exit(1)
	}

	cancel()
}
