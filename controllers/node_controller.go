package controllers

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// NodeReconciler reconciles a Node object
type NodeReconciler struct {
	nodes     map[string]string
	nodesLock chan bool
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
func (r *NodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

// GetNodesByIP returns the actual set of nodes
func (r *NodeReconciler) GetNodesByIP() map[string]string {
	r.nodesLock <- true
	defer func() {
		<-r.nodesLock
	}()

	nodes := map[string]string{}
	for k, v := range r.nodes {
		nodes[v] = k
	}

	return nodes
}

// SetupWithManager sets up the controller with the Manager.
func (r *NodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.nodes = map[string]string{}
	r.nodesLock = make(chan bool, 1)

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		Watches(&source.Kind{Type: &corev1.Node{}}, nodeEventHandler{r}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		Complete(r)
}

type nodeEventHandler struct {
	*NodeReconciler
}

func (eh nodeEventHandler) Create(e event.CreateEvent, _ workqueue.RateLimitingInterface) {
	eh.nodesLock <- true
	defer func() {
		<-eh.nodesLock
	}()

	node, ok := e.Object.(*corev1.Node)
	if !ok {
		panic("Invalid Node object type")
	}

	var nodeName, nodeIP string
	for i := range node.Status.Addresses {
		if node.Status.Addresses[i].Type == corev1.NodeHostName {
			nodeName = node.Status.Addresses[i].Address
		} else if node.Status.Addresses[i].Type == corev1.NodeInternalIP {
			nodeIP = node.Status.Addresses[i].Address
		}
	}

	eh.nodes[nodeName] = nodeIP
}

// Update detects StorageOS related config changes.
func (eh nodeEventHandler) Update(e event.UpdateEvent, r workqueue.RateLimitingInterface) {
	eh.Create(event.CreateEvent{Object: e.ObjectNew}, r)
}

func (eh nodeEventHandler) Delete(e event.DeleteEvent, _ workqueue.RateLimitingInterface) {
	eh.nodesLock <- true
	defer func() {
		<-eh.nodesLock
	}()

	delete(eh.nodes, e.Object.GetName())
}

func (eh nodeEventHandler) Generic(event.GenericEvent, workqueue.RateLimitingInterface) {}
