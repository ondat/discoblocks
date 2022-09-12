package controllers

import (
	"context"
	"errors"

	"github.com/go-logr/logr"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

// JobReconciler reconciles a Job object
type JobReconciler struct {
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
func (r *JobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return ctrl.Result{}, r.Client.Delete(ctx, &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
		},
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *JobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&batchv1.Job{}).
		WithEventFilter(jobEventFilter{logger: mgr.GetLogger().WithName("JobReconciler")}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		Complete(r)
}

type jobEventFilter struct {
	logger logr.Logger
}

func (ef jobEventFilter) Create(_ event.CreateEvent) bool {
	return false
}

func (ef jobEventFilter) Delete(_ event.DeleteEvent) bool {
	return false
}

func (ef jobEventFilter) Update(e event.UpdateEvent) bool {
	newObj, ok := e.ObjectNew.(*batchv1.Job)
	if !ok {
		ef.logger.Error(errors.New("unsupported type"), "Unable to cast old object")
		return false
	}

	if app, ok := newObj.Labels["app"]; !ok || app != "discoblocks" {
		return false
	}

	return newObj.Status.CompletionTime != nil && newObj.Status.Succeeded == 1
}

func (ef jobEventFilter) Generic(_ event.GenericEvent) bool {
	return false
}
