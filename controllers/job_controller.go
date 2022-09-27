package controllers

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
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
	logger := log.FromContext(ctx).WithName("JobReconciler").WithValues("req_name", req.Name, "namespace", req.Name)

	logger.Info("Reconcile job...")
	defer logger.Info("Reconciled")

	label, err := labels.NewRequirement("job-name", selection.Equals, []string{req.Name})
	if err != nil {
		logger.Error(err, "Unable to parse Job label selector")
		return ctrl.Result{}, nil
	}
	jobSelector := labels.NewSelector().Add(*label)

	logger.Info("Fetch Pods...")

	podList := corev1.PodList{}
	if err = r.List(ctx, &podList, &client.ListOptions{
		Namespace:     req.Namespace,
		LabelSelector: jobSelector,
	}); err != nil {
		logger.Info("Failed to list Jobs", "error", err.Error())
		return ctrl.Result{}, fmt.Errorf("unable to list Jobs: %w", err)
	}

	for i := range podList.Items {
		logger.Info("Delete Pod...", "pod_name", podList.Items[i].Name)

		if err := r.Client.Delete(ctx, &podList.Items[i]); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}

			logger.Info("Failed to delete pod", "pod_name", podList.Items[i].Name, "error", err.Error())
			return ctrl.Result{}, fmt.Errorf("unable to delete pod %s: %w", podList.Items[i].Name, err)
		}
	}

	logger.Info("Delete Job...")

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
