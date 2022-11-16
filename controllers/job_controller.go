package controllers

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/ondat/discoblocks/pkg/metrics"
	"github.com/ondat/discoblocks/pkg/utils"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// JobReconciler reconciles a Job object
type JobReconciler struct {
	EventService utils.EventService
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

	logger.Info("Reconcile Job...")
	defer logger.Info("Reconciled")

	logger.Info("Fetch Job...")

	job := batchv1.Job{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: req.Name, Namespace: req.Namespace}, &job); err != nil {
		if !apierrors.IsNotFound(err) {
			metrics.NewError("Job", req.Name, req.Namespace, "Kube API", "get")

			return ctrl.Result{}, fmt.Errorf("failed to fetch job %s/%s: %w", req.Namespace, req.Name, err)
		}
	}

	if job.UID != "" {
		succeeded := job.Status.Succeeded == 1

		operation, podName, pvcName := job.Annotations["discoblocks/operation"], job.Annotations["discoblocks/pod"], job.Annotations["discoblocks/pvc"]
		if operation != "" && podName != "" && pvcName != "" {
			pod := corev1.Pod{}
			if err := r.Client.Get(ctx, types.NamespacedName{Name: podName, Namespace: req.Namespace}, &pod); err != nil {
				if !apierrors.IsNotFound(err) {
					metrics.NewError("Pod", podName, req.Namespace, "Kube API", "get")

					logger.Error(err, "Failed to fetch Pod", "pod_name", podName)
					return ctrl.Result{}, fmt.Errorf("failed to fetch pod %s/%s: %w", req.Namespace, podName, err)
				}
			} else {
				capacity := "?"

				pvc := &corev1.PersistentVolumeClaim{}
				if err := r.Client.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: req.Namespace}, pvc); err != nil {
					if !apierrors.IsNotFound(err) {
						metrics.NewError("PersistentVolumeClaim", pvcName, req.Namespace, "Kube API", "get")

						logger.Error(err, "Failed to fetch PVC", "pvc_name", pvcName)
						return ctrl.Result{}, fmt.Errorf("failed to fetch PVC %s/%s: %w", req.Namespace, pvcName, err)
					}

					logger.Error(err, "PVC not found", "pvc_name", pvcName)

					pvc = nil
				} else {
					r := pvc.Status.Capacity[corev1.ResourceStorage]
					capacity = r.String()
				}

				if succeeded {
					if err := r.EventService.SendNormal(req.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("New capacity of %s: %s", pvcName, capacity), fmt.Sprintf("Operation finished: %s", operation), &pod, pvc); err != nil {
						metrics.NewError("Event", "", "", "Kube API", "create")

						logger.Error(err, "Failed to create event")
					}
				} else {
					logger.Error(errors.New("job has failed"), "Job failed")

					if err := r.EventService.SendWarning(req.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Failed to apply new capacity of %s: %s", pvcName, capacity), fmt.Sprintf("Operation finished: %s", operation), &pod, pvc); err != nil {
						metrics.NewError("Event", "", "", "Kube API", "create")

						logger.Error(err, "Failed to create event")
					}
				}
			}
		}

		if !succeeded {
			return ctrl.Result{}, nil
		}
	}

	label, err := labels.NewRequirement("job-name", selection.Equals, []string{req.Name})
	if err != nil {
		logger.Error(err, "Unable to parse Job label selector")
		return ctrl.Result{}, nil
	}
	jobSelector := labels.NewSelector().Add(*label)

	logger.Info("Fetch Pods...")

	podList := corev1.PodList{}
	if err = r.Client.List(ctx, &podList, &client.ListOptions{
		Namespace:     req.Namespace,
		LabelSelector: jobSelector,
	}); err != nil {
		metrics.NewError("Pod", "", req.Namespace, "Kube API", "list")

		logger.Info("Failed to list Jobs", "error", err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to list Jobs: %w", err)
	}

	for i := range podList.Items {
		logger.Info("Delete Pod...", "pod_name", podList.Items[i].Name)

		if err := r.Client.Delete(ctx, &podList.Items[i]); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}

			metrics.NewError("Pod", podList.Items[i].Name, podList.Items[i].Namespace, "Kube API", "delete")

			logger.Info("Failed to delete pod", "pod_name", podList.Items[i].Name, "error", err.Error())
			return ctrl.Result{}, fmt.Errorf("failed to delete Pod %s/%s: %w", req.Namespace, podList.Items[i].Name, err)
		}
	}

	logger.Info("Delete Job...")

	if err = r.Client.Delete(ctx, &job); err != nil && !apierrors.IsNotFound(err) {
		metrics.NewError("Job", job.Name, job.Namespace, "Kube API", "delete")

		return ctrl.Result{}, fmt.Errorf("failed to delete Job %s/%s: %w", req.Namespace, job.Name, err)
	}

	return ctrl.Result{}, nil
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

	return newObj.Status.CompletionTime != nil
}

func (ef jobEventFilter) Generic(_ event.GenericEvent) bool {
	return false
}
