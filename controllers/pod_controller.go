package controllers

import (
	"context"
	"errors"
	"strings"

	"github.com/go-logr/logr"
	"github.com/ondat/discoblocks/pkg/utils"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// PodReconciler reconciles a Pod object
type PodReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// Modify the Reconcile function to compare the state specified by
// the DiskConfig object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.0/pkg/reconcile
func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx).WithName("PodReconciler").WithValues("name", req.Name, "namespace", req.Name)

	serviceName, err := utils.RenderResourceName(req.Name, req.Namespace)
	if err != nil {
		logger.Error(err, "Failed to render resource name")
		return ctrl.Result{}, nil
	}

	service, err := utils.RenderMetricsService(serviceName, req.Namespace)
	if err != nil {
		logger.Error(err, "Failed to render Service")
		return ctrl.Result{}, nil
	}

	service.Labels = map[string]string{
		"discoblocks": req.Name,
	}
	service.Spec.Selector = map[string]string{
		utils.RenderMetricsLabel(req.Name): req.Name,
	}

	pod := &corev1.Pod{}
	if err = r.Client.Get(ctx, types.NamespacedName{Name: req.Name, Namespace: req.Namespace}, pod); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Error(err, "Failed to find Pod")
			return ctrl.Result{}, nil
		}

		logger.Error(err, "Failed to get Pod")
		return ctrl.Result{}, err
	}

	service.OwnerReferences = []metav1.OwnerReference{
		{
			APIVersion: pod.APIVersion,
			Kind:       pod.Kind,
			Name:       pod.Name,
			UID:        pod.UID,
		},
	}

	logger.Info("Create Service...")
	if err = r.Client.Create(ctx, service); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			logger.Error(err, "Failed to create Service")
			return ctrl.Result{}, err
		}

		logger.Info("Service already exists")
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		WithEventFilter(podEventFilter{logger: mgr.GetLogger().WithName("PodReconciler")}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		Complete(r)
}

type podEventFilter struct {
	logger logr.Logger
}

func (ef podEventFilter) Create(e event.CreateEvent) bool {
	obj, ok := e.Object.(*corev1.Pod)
	if !ok {
		ef.logger.Error(errors.New("unsupported type"), "Unable to cast old object")
		return false
	}

	for k := range obj.Labels {
		if strings.HasPrefix(k, "discoblocks-metrics/") {
			return true
		}
	}

	return false
}

func (ef podEventFilter) Delete(_ event.DeleteEvent) bool {
	return false
}

func (ef podEventFilter) Update(_ event.UpdateEvent) bool {
	return false
}

func (ef podEventFilter) Generic(_ event.GenericEvent) bool {
	return false
}
