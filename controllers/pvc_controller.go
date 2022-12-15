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

package controllers

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	discoblocksondatiov1 "github.com/ondat/discoblocks/api/v1"
	"github.com/ondat/discoblocks/pkg/diskinfo"
	"github.com/ondat/discoblocks/pkg/drivers"
	"github.com/ondat/discoblocks/pkg/metrics"
	"github.com/ondat/discoblocks/pkg/utils"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const monitoringPeriod = time.Minute / 2

type nodeCache interface {
	GetNodesByIP() map[string]string
}

// PVCReconciler reconciles a PVC object
type PVCReconciler struct {
	EventService utils.EventService
	NodeCache    nodeCache
	InProgress   sync.Map
	client.Client
	Scheme *runtime.Scheme
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// Modify the Reconcile function to compare the state specified by
// the PVC object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.0/pkg/reconcile
func (r *PVCReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx).WithName("PVCReconciler").WithValues("req_name", req.Name, "namespace", req.Name)

	lock, unlock := controllerSemaphore()
	if !lock {
		logger.Info("Another operation is on going, event needs to be resceduled")
		return ctrl.Result{Requeue: true}, nil
	}
	defer unlock()

	logger.Info("Reconciling...")
	defer logger.Info("Reconciled")

	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	pvc := corev1.PersistentVolumeClaim{}
	if err := r.Get(ctx, req.NamespacedName, &pvc); err != nil {
		if !apierrors.IsNotFound(err) {
			metrics.NewError("PersistentVolumeClaim", req.Name, req.Namespace, "Kube API", "get")

			return ctrl.Result{}, fmt.Errorf("unable to fetch PVC: %w", err)
		}

		logger.Info("PVC not found")

		return ctrl.Result{}, nil
	}

	logger.Info("Fetch DiskConfig...")

	config := discoblocksondatiov1.DiskConfig{}
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: pvc.Namespace, Name: pvc.Labels["discoblocks"]}, &config); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("DiskConfig not found")

			return ctrl.Result{}, nil
		}

		metrics.NewError("DiskConfig", pvc.Labels["discoblocks"], pvc.Namespace, "Kube API", "get")

		logger.Info("Unable to fetch PVC", "error", err.Error())
		return ctrl.Result{}, errors.New("unable to fetch PVC")
	}
	logger = logger.WithValues("dc_name", config.Name)

	reason := "PvcPhaseHasChanged"

	if pvc.DeletionTimestamp != nil {
		toDelete := []int{}

		for i := range config.Status.Conditions {
			if config.Status.Conditions[i].Reason != reason ||
				config.Status.Conditions[i].Message != pvc.Name {
				continue
			}

			toDelete = append(toDelete, i)
		}

		sort.Ints(toDelete)

		for i, d := range toDelete {
			d -= i

			config.Status.Conditions = append(config.Status.Conditions[:d], config.Status.Conditions[d+1:]...)
		}
	} else {
		if config.Status.Conditions == nil {
			config.Status.Conditions = []metav1.Condition{}
		}

		toUpdate := -1
		for i := range config.Status.Conditions {
			if config.Status.Conditions[i].Reason != reason ||
				config.Status.Conditions[i].Message != pvc.Name {
				continue
			}

			toUpdate = i
			break
		}

		logger.Info("Add status", "phase", pvc.Status.Phase)

		status := metav1.ConditionFalse
		if pvc.Status.Phase == corev1.ClaimBound {
			status = metav1.ConditionTrue
		}

		condition := metav1.Condition{
			Status:             status,
			Type:               string(pvc.Status.Phase),
			ObservedGeneration: pvc.Generation,
			LastTransitionTime: metav1.NewTime(time.Now()),
			Reason:             reason,
			Message:            pvc.Name,
		}

		if toUpdate == -1 {
			config.Status.Conditions = append(config.Status.Conditions, condition)
		} else {
			config.Status.Conditions[toUpdate] = condition
		}
	}

	logger.Info("Update DiskConfig status...")

	if err := r.Client.Status().Update(ctx, &config); err != nil {
		metrics.NewError("DiskConfig", config.Name, config.Namespace, "Kube API", "update")

		logger.Info("Unable to update PVC status", "error", err.Error())
		return ctrl.Result{}, errors.New("unable to update PVC status")
	}

	logger.Info("Updated")

	return ctrl.Result{}, nil
}

// MonitorVolumes monitors volumes periodycally
//nolint:gocyclo // It is complex we know
func (r *PVCReconciler) MonitorVolumes() {
	logger := logf.Log.WithName("VolumeMonitor")

	logger.Info("Monitor Volumes...")
	defer logger.Info("Monitor done")

	ctx, cancel := context.WithTimeout(context.Background(), monitoringPeriod)
	defer cancel()

	logger.Info("Fetch DiskConfigs...")

	diskConfigs := discoblocksondatiov1.DiskConfigList{}
	if err := r.Client.List(ctx, &diskConfigs); err != nil {
		metrics.NewError("DiskConfig", "", "", "Kube API", "list")

		logger.Error(err, "Unable to fetch DiskConfigs")
		return
	}

	for d := range diskConfigs.Items {
		config := diskConfigs.Items[d]

		if config.Spec.Policy.Pause {
			logger.Info("Autoscaling paused")
			continue
		}

		last, loaded := r.InProgress.Load(config.Name)
		if loaded && last.(time.Time).Add(config.Spec.Policy.CoolDown.Duration).After(time.Now()) {
			logger.Info("Autoscaling cooldown")
			continue
		}

		logger := logger.WithValues("dc_name", config.Name, "dc_namespace", config.Namespace)

		configLabel, err := labels.NewRequirement("discoblocks", selection.Equals, []string{config.Name})
		if err != nil {
			logger.Error(err, "Unable to parse PVC label selector")
			continue
		}
		pvcSelector := labels.NewSelector().Add(*configLabel)

		logger.Info("Fetch PVCs...")

		pvcs := corev1.PersistentVolumeClaimList{}
		if err = r.Client.List(ctx, &pvcs, &client.ListOptions{
			Namespace:     config.Namespace,
			LabelSelector: pvcSelector,
		}); err != nil {
			metrics.NewError("PersistentVolumeClaim", "", config.Namespace, "Kube API", "list")

			logger.Error(err, "Unable to fetch PVCs")
			continue
		}

		activePVCs := []*corev1.PersistentVolumeClaim{}
		for i := range pvcs.Items {
			if pvcs.Items[i].DeletionTimestamp != nil ||
				!controllerutil.ContainsFinalizer(&pvcs.Items[i], utils.RenderFinalizer(config.Name)) ||
				pvcs.Items[i].Status.ResizeStatus != nil && *pvcs.Items[i].Status.ResizeStatus != corev1.PersistentVolumeClaimNoExpansionInProgress {
				continue
			}

			activePVCs = append(activePVCs, &pvcs.Items[i])

			logger.Info("Volume found", "pvc_name", pvcs.Items[i].Name)
		}

		if len(activePVCs) == 0 {
			logger.Info("Unable to find any PVC")
			continue
		}

		podLabel, err := labels.NewRequirement(utils.RenderUniqueLabel(string(config.UID)), selection.Equals, []string{config.Name})
		if err != nil {
			logger.Error(err, "Unable to parse Pod label selector")
			continue
		}
		podSelector := labels.NewSelector().Add(*podLabel)

		logger.Info("Fetch Pods...")

		pods := corev1.PodList{}
		if err = r.Client.List(ctx, &pods, &client.ListOptions{
			Namespace:     config.Namespace,
			LabelSelector: podSelector,
		}); err != nil {
			metrics.NewError("Pod", "", config.Namespace, "Kube API", "list")

			logger.Error(err, "Unable to fetch Pods")
			continue
		}

		sem := utils.CreateSemaphore(concurrency, config.Spec.Policy.CoolDown.Duration)
		wg := sync.WaitGroup{}

		for p := range pods.Items {
			pod := pods.Items[p]

			// Skip monitoring of new Pods
			if pod.DeletionTimestamp != nil || pod.CreationTimestamp.Add(config.Spec.Policy.CoolDown.Duration).After(time.Now()) {
				continue
			}

			wg.Add(1)

			go func() {
				defer wg.Done()

				unlock, err := utils.WaitForSemaphore(ctx, sem)
				if err != nil {
					metrics.NewError("VolumeMonitor", "", "", "DiscoBlocks", "semaphore")

					logger.Info("Context deadline")
					return
				}
				defer unlock()

				logger := logger.WithValues("pod_name", pod.Name)

				logger.Info("Fetch DiskInfo...")

				diskInfo, err := diskinfo.Fetch(pod.Name, pod.Namespace)
				if err != nil {
					metrics.NewError("Pod", pod.Name, pod.Namespace, "DiscoBlocks", "metrics")

					logger.Error(err, "Unable to fetch disk info")

					if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", "Failed to fetch disk info", err.Error(), &pod, nil); err != nil {
						metrics.NewError("Event", "", "", "Kube API", "create")

						logger.Error(err, "Failed to create event")
					}

					return
				}

				podPVCsByParent := map[string][]*corev1.PersistentVolumeClaim{}
				for i := range pod.Spec.Volumes {
					if pod.Spec.Volumes[i].PersistentVolumeClaim == nil {
						continue
					}

					for cp := range activePVCs {
						if _, ok := activePVCs[cp].Labels["discoblocks-parent"]; !ok &&
							pod.Spec.Volumes[i].PersistentVolumeClaim.ClaimName == activePVCs[cp].Name {
							if _, ok := podPVCsByParent[activePVCs[cp].Name]; !ok {
								podPVCsByParent[activePVCs[cp].Name] = []*corev1.PersistentVolumeClaim{}
							}
							podPVCsByParent[activePVCs[cp].Name] = append(podPVCsByParent[activePVCs[cp].Name], activePVCs[cp])

							for cc := range activePVCs {
								if parent, ok := activePVCs[cc].Labels["discoblocks-parent"]; ok && parent == activePVCs[cp].Name {
									podPVCsByParent[activePVCs[cp].Name] = append(podPVCsByParent[activePVCs[cp].Name], activePVCs[cc])
								}
							}
						}
					}
				}

				if len(podPVCsByParent) == 0 {
					logger.Error(err, "Unable to find any PVC for Pod")
					return
				}

				for _, pvcFamily := range podPVCsByParent {
					sort.Slice(pvcFamily, func(i, j int) bool {
						return pvcFamily[i].CreationTimestamp.UnixNano() < pvcFamily[j].CreationTimestamp.UnixNano()
					})

					lastPVC := pvcFamily[len(pvcFamily)-1]

					actIndex := 0
					if lastIndex, ok := lastPVC.Labels["discoblocks-index"]; ok {
						actIndex, err = strconv.Atoi(lastIndex)
						if err != nil {
							metrics.NewError("Pod", pod.Name, pod.Namespace, "DiscoBlocks", "lastindex")

							logger.Error(err, "Unable to convert index")

							if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Failed to convert last index of %s: %s", lastPVC.Name, lastIndex), err.Error(), &pod, nil); err != nil {
								metrics.NewError("Event", "", "", "Kube API", "create")

								logger.Error(err, "Failed to create event")
							}

							continue
						}
					}

					lastMountPoint := utils.RenderMountPoint(config.Spec.MountPointPattern, config.Name, actIndex)

					logger = logger.WithValues("last_pvc", lastPVC.Name, "last_pv", lastPVC.Spec.VolumeName, "last_mp", lastMountPoint)

					lastUsed, ok := diskInfo[lastMountPoint]
					if !ok {
						metrics.NewError("Pod", pod.Name, pod.Namespace, "DiscoBlocks", "last_mount_point")

						logger.Error(err, "Unable to find metrics", "disk_info", diskInfo)

						if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Failed to find metrics of %s: %s", lastPVC.Name, lastMountPoint), "Unable to find metrics", &pod, nil); err != nil {
							metrics.NewError("Event", "", "", "Kube API", "create")

							logger.Error(err, "Failed to create event")
						}

						continue
					}

					logger = logger.WithValues("last_used_%", lastUsed)

					if lastUsed < float64(config.Spec.Policy.UpscaleTriggerPercentage) {
						logger.Info("Disk size ok")
						continue
					}

					newCapacity := config.Spec.Policy.ExtendCapacity
					newCapacity.Add(lastPVC.Spec.Resources.Requests[corev1.ResourceStorage])

					logger = logger.WithValues("new_capacity", newCapacity.String(), "max_capacity", config.Spec.Policy.MaximumCapacityOfDisk.String(), "no_disks", len(pvcFamily), "max_disks", config.Spec.Policy.MaximumNumberOfDisks)

					logger.Info("Find Node name")

					nodeName := r.NodeCache.GetNodesByIP()[pod.Status.HostIP]
					if nodeName == "" {
						metrics.NewError("Node", pod.Status.HostIP, "", "DiscoBlocks", "cache")

						logger.Error(errors.New("node not found: "+pod.Status.HostIP), "Node not found", "IP", pod.Status.HostIP)

						if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Node not found for %s: %s", lastPVC.Name, pod.Status.HostIP), err.Error(), &pod, nil); err != nil {
							metrics.NewError("Event", "", "", "Kube API", "create")

							logger.Error(err, "Failed to create event")
						}

						continue
					}

					logger = logger.WithValues("node_name", nodeName)

					if newCapacity.Cmp(config.Spec.Policy.MaximumCapacityOfDisk) == 1 {
						if config.Spec.Policy.MaximumNumberOfDisks > 0 && len(pvcFamily) >= int(config.Spec.Policy.MaximumNumberOfDisks) {
							logger.Info("Already maximum number of disks", "number", config.Spec.Policy.MaximumNumberOfDisks)
							continue
						}

						logger.Info("New disk needed")

						nextIndex := actIndex + 1

						logger.Info("Next index", "index", nextIndex)

						containerIDs := []string{}
						for i := range pod.Status.ContainerStatuses {
							cID := pod.Status.ContainerStatuses[i].ContainerID
							for _, prefix := range []string{"containerd://", "docker://"} {
								cID = strings.TrimPrefix(cID, prefix)
							}

							containerIDs = append(containerIDs, cID)
						}

						r.InProgress.Store(config.Name, time.Now())

						go r.createPVC(&config, &pod, pvcFamily[0], containerIDs, nodeName, nextIndex, logger)

						continue
					}

					logger.Info("Resize needed")

					r.InProgress.Store(config.Name, time.Now())

					go r.resizePVC(&config, &pod, newCapacity, lastPVC, nodeName, logger)
				}
			}()
		}

		wg.Wait()
	}
}

//nolint:gocyclo // It is complex we know
func (r *PVCReconciler) createPVC(config *discoblocksondatiov1.DiskConfig, pod *corev1.Pod, parentPVC *corev1.PersistentVolumeClaim, containerIDs []string, nodeName string, nextIndex int, logger logr.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	logger.Info("Fetch StorageClass...")

	sc := storagev1.StorageClass{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: config.Spec.StorageClassName}, &sc); err != nil {
		if apierrors.IsNotFound(err) {
			metrics.NewError("StorageClass", config.Spec.StorageClassName, "", "Kube API", "get")

			logger.Error(err, "StorageClass not found", "sc_name", config.Spec.StorageClassName)

			if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("StorageClass not found for %s: %s", config.Name, config.Spec.StorageClassName), err.Error(), pod, config); err != nil {
				metrics.NewError("Event", "", "", "Kube API", "create")

				logger.Error(err, "Failed to create event")
			}

			return
		}

		logger.Error(err, "Unable to fetch StorageClass")

		if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Failed to fetch StorageClass for %s: %s", config.Name, config.Spec.StorageClassName), err.Error(), pod, config); err != nil {
			metrics.NewError("Event", "", "", "Kube API", "create")

			logger.Error(err, "Failed to create event")
		}

		return
	}
	logger = logger.WithValues("provisioner", sc.Provisioner)

	logger.Info("Fetch Node...")

	node := &corev1.Node{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: nodeName}, node); err != nil {
		metrics.NewError("Node", nodeName, "", "Kube API", "get")

		logger.Error(err, "Failed to get Node")

		if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Failed to fetch Node for %s: %s", config.Name, nodeName), err.Error(), pod, config); err != nil {
			metrics.NewError("Event", "", "", "Kube API", "create")

			logger.Error(err, "Failed to create event")
		}

		return
	}

	driver := drivers.GetDriver(sc.Provisioner)
	if driver == nil {
		metrics.NewError("CSI", sc.Provisioner, "", sc.Provisioner, "GetDriver")

		dmErr := errors.New("driver not found: " + sc.Provisioner)

		logger.Error(dmErr, "Driver not found")

		if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Failed to find driver for %s: %s", config.Name, sc.Provisioner), dmErr.Error(), pod, config); err != nil {
			metrics.NewError("Event", "", "", "Kube API", "create")

			logger.Error(err, "Failed to create event")
		}

		return
	}

	prefix := utils.GetNamePrefix(discoblocksondatiov1.ReadWriteOnce, string(config.UID), nodeName)

	pvcName, err := utils.RenderResourceName(true, prefix, config.Name, config.Namespace)
	if err != nil {
		logger.Error(err, "Failed to render PersistentVolumeClaim name")
		return
	}

	pvc, err := driver.GetPVCStub(pvcName, config.Namespace, config.Spec.StorageClassName)
	if err != nil {
		metrics.NewError("CSI", pvcName, "", sc.Provisioner, "GetPVCStub")

		logger.Error(err, "Failed to call driver", "method", "GetPVCStub")

		if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Failed to call driver.GetPVCStub for %s: %s", config.Name, sc.Provisioner), err.Error(), pod, config); err != nil {
			metrics.NewError("Event", "", "", "Kube API", "create")

			logger.Error(err, "Failed to create event")
		}

		return
	}
	logger = logger.WithValues("pvc_name", pvc.Name)

	utils.PVCDecorator(config, prefix, driver, pvc)

	scAllowedTopology, err := driver.GetStorageClassAllowedTopology(node)
	if err != nil {
		metrics.NewError("CSI", node.Name, "", sc.Provisioner, "GetStorageClassAllowedTopology")

		logger.Error(err, "Failed to call driver", "method", "GetStorageClassAllowedTopology")

		if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Failed to call driver.GetStorageClassAllowedTopology for %s: %s", config.Name, sc.Provisioner), err.Error(), pod, config); err != nil {
			metrics.NewError("Event", "", "", "Kube API", "create")

			logger.Error(err, "Failed to create event")
		}

		return
	}

	waitForMeta, err := driver.WaitForVolumeAttachmentMeta()
	if err != nil {
		metrics.NewError("CSI", "", "", sc.Provisioner, "WaitForVolumeAttachmentMeta")

		logger.Error(err, "Failed to call driver", "method", "WaitForVolumeAttachmentMeta")

		if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Failed to call driver.WaitForVolumeAttachmentMeta %s: %s", config.Name, sc.Provisioner), err.Error(), pod, config); err != nil {
			metrics.NewError("Event", "", "", "Kube API", "create")

			logger.Error(err, "Failed to create event")
		}

		return
	}

	if len(scAllowedTopology) != 0 {
		topologySC, err := utils.NewStorageClass(&sc, scAllowedTopology)
		if err != nil {
			logger.Error(err, "Failed to render get NewStorageClass")

			if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Failed to create new StorageClass for %s", config.Name), err.Error(), pod, config); err != nil {
				metrics.NewError("Event", "", "", "Kube API", "create")

				logger.Error(err, "Failed to create event")
			}

			return
		}

		logger.Info("Create StorageClass...")

		if err = r.Client.Create(ctx, topologySC); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				metrics.NewError("StorageClass", topologySC.Name, "", "Kube API", "create")

				logger.Error(err, "Failed to create topology StorageClass")

				if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Failed to create StorageClass for %s: %s", config.Name, topologySC.Name), err.Error(), pod, config); err != nil {
					metrics.NewError("Event", "", "", "Kube API", "create")

					logger.Error(err, "Failed to create event")
				}

				return
			}

			logger.Info("Fetch topology StorageClass...")

			if err = r.Client.Get(ctx, types.NamespacedName{Name: topologySC.Name}, topologySC); err != nil {
				metrics.NewError("StorageClass", topologySC.Name, "", "Kube API", "get")

				logger.Error(err, "Failed to fetch topology StorageClass")

				if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Failed to fetch StorageClass for %s: %s", config.Name, topologySC.Name), err.Error(), pod, config); err != nil {
					metrics.NewError("Event", "", "", "Kube API", "create")

					logger.Error(err, "Failed to create event")
				}

				return
			}

			scFinalizer := utils.RenderFinalizer(config.Name, config.Namespace)
			if !controllerutil.ContainsFinalizer(topologySC, scFinalizer) {
				controllerutil.AddFinalizer(topologySC, scFinalizer)

				logger.Info("Update topology StorageClass finalizer...", "name", topologySC.Name)

				if err = r.Client.Update(ctx, topologySC); err != nil {
					metrics.NewError("StorageClass", topologySC.Name, "", "Kube API", "update")

					logger.Error(err, "Failed to update topology StorageClass")

					if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Failed to update StorageClass for %s: %s", config.Name, topologySC.Name), err.Error(), pod, config); err != nil {
						metrics.NewError("Event", "", "", "Kube API", "create")

						logger.Error(err, "Failed to create event")
					}

					return
				}
			}
		}

		pvc.Spec.StorageClassName = &topologySC.Name
	}

	pvc.Labels["discoblocks-parent"] = parentPVC.Name
	pvc.Labels["discoblocks-index"] = fmt.Sprintf("%d", nextIndex)

	pvc.OwnerReferences = []metav1.OwnerReference{
		{
			APIVersion: parentPVC.APIVersion,
			Kind:       parentPVC.Kind,
			Name:       parentPVC.Name,
			UID:        parentPVC.UID,
		},
	}

	logger.Info("Create PVC...")

	if err = r.Client.Create(ctx, pvc); err != nil {
		metrics.NewError("PersistentVolume", pvc.Name, pvc.Namespace, "Kube API", "create")

		logger.Error(err, "Failed to create PVC")

		if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Failed to create PVC for %s: %s", config.Name, pvc.Name), err.Error(), pod, config); err != nil {
			metrics.NewError("Event", "", "", "Kube API", "create")

			logger.Error(err, "Failed to create event")
		}

		return
	}
	metrics.NewPVCOperation(pvc.Name, pvc.Namespace, "create", config.Spec.Capacity.String())

	waitCtx, cancel := context.WithTimeout(context.Background(), config.Spec.Policy.CoolDown.Duration)
	defer cancel()

	logger.Info("Wait PersistentVolume...")

	var waitPVErr error
WAIT_PV:
	for {
		select {
		case <-waitCtx.Done():
			metrics.NewError("PersistentVolume", pvc.Name, pvc.Namespace, "Kube API", "get")

			if waitPVErr == nil {
				waitPVErr = waitCtx.Err()
			}

			logger.Error(waitPVErr, "PV creation wait timeout")

			if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("PV creation wait timeout for %s: %s", config.Name, pvc.Name), waitPVErr.Error(), pod, pvc); err != nil {
				metrics.NewError("Event", "", "", "Kube API", "create")

				logger.Error(err, "Failed to create event")
			}

			return
		default:
			if waitPVErr = r.Client.Get(ctx, types.NamespacedName{Namespace: pvc.Namespace, Name: pvc.Name}, pvc); waitPVErr == nil &&
				pvc.Spec.VolumeName != "" {
				break WAIT_PV
			}

			<-time.NewTimer(time.Second).C
		}
	}

	logger.Info("Wait CSI provision...")

	pv := &corev1.PersistentVolume{}

	var waitCSIErr error
WAIT_CSI:
	for {
		select {
		case <-waitCtx.Done():
			metrics.NewError("PersistentVolume", pvc.Spec.VolumeName, "", "Kube API", "get")

			if waitCSIErr == nil {
				waitCSIErr = waitCtx.Err()
			}

			logger.Error(waitCSIErr, "CSI provision wait timeout")

			if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("CSI provision wait timeout for %s: %s", config.Name, pv.Name), waitCSIErr.Error(), pod, pv); err != nil {
				metrics.NewError("Event", "", "", "Kube API", "create")

				logger.Error(err, "Failed to create event")
			}

			return
		default:
			if waitCSIErr = r.Client.Get(ctx, types.NamespacedName{Name: pvc.Spec.VolumeName}, pv); waitCSIErr == nil &&
				pv.Spec.CSI != nil && pv.Spec.CSI.VolumeHandle != "" {
				break WAIT_CSI
			}

			<-time.NewTimer(time.Second).C
		}
	}

	vaName, err := utils.RenderResourceName(true, config.Name, pvc.Name, pvc.Namespace)
	if err != nil {
		logger.Error(err, "Failed to render VolumeAttachment name")
		return
	}

	volumeAttachment := &storagev1.VolumeAttachment{
		ObjectMeta: metav1.ObjectMeta{
			Name: vaName,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: pod.APIVersion,
					Kind:       pod.Kind,
					Name:       pod.Name,
					UID:        pod.UID,
				},
			},
		},
		Spec: storagev1.VolumeAttachmentSpec{
			Attacher: sc.Provisioner,
			Source: storagev1.VolumeAttachmentSource{
				PersistentVolumeName: &pvc.Spec.VolumeName,
			},
			NodeName: nodeName,
		},
	}

	logger.Info("Create VolumeAttachment...", "attacher", sc.Provisioner, "node_name", nodeName)

	if err = r.Client.Create(ctx, volumeAttachment); err != nil {
		metrics.NewError("VolumeAttachment", volumeAttachment.Name, "", "Kube API", "create")

		logger.Error(err, "Failed to create volume attachment")

		if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Failed to create volume attachment for %s: %s", config.Name, pv.Name), err.Error(), pod, pv); err != nil {
			metrics.NewError("Event", "", "", "Kube API", "create")

			logger.Error(err, "Failed to create event")
		}

		return
	}

	volumeMeta := ""
	if waitForMeta != "" {
		logger.Info("Wait VolumeAttachment...", "waitForMeta", waitForMeta)

		var waitVAErr error
	WAIT_VA:
		for {
			select {
			case <-waitCtx.Done():
				metrics.NewError("VolumeAttachment", "", "", "Kube API", "list")

				if waitVAErr == nil {
					waitVAErr = waitCtx.Err()
				}

				logger.Error(waitVAErr, "VolumeAttachment wait timeout")

				if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("VolumeAttachment wait timeout for %s: %s", config.Name, volumeAttachment.Name), waitVAErr.Error(), pod, volumeAttachment); err != nil {
					metrics.NewError("Event", "", "", "Kube API", "create")

					logger.Error(err, "Failed to create event")
				}

				return
			default:
				volumeAttachment, waitVAErr = r.getVolumeAttachment(ctx, pvc.Spec.VolumeName)
				if err != nil ||
					volumeAttachment == nil ||
					!volumeAttachment.Status.Attached ||
					volumeAttachment.Status.AttachmentMetadata[waitForMeta] == "" {
					<-time.NewTimer(time.Second).C
					continue
				}

				volumeMeta = volumeAttachment.Status.AttachmentMetadata[waitForMeta]

				logger.Info("VolumeAttachment meta has found", "waitForMeta", waitForMeta, "value", volumeMeta)

				break WAIT_VA
			}
		}
	}

	preMountCmd, err := driver.GetPreMountCommand(pv, volumeAttachment)
	if err != nil {
		metrics.NewError("CSI", pv.Name, "", sc.Provisioner, "GetPreMountCommand")

		logger.Error(err, "Failed to call driver", "method", "GetPreMountCommand")

		if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Failed to call driver.GetPreMountCommand for %s: %s", config.Name, sc.Provisioner), err.Error(), pod, config); err != nil {
			metrics.NewError("Event", "", "", "Kube API", "create")

			logger.Error(err, "Failed to create event")
		}

		return
	}

	mountpoint := utils.RenderMountPoint(config.Spec.MountPointPattern, config.Name, nextIndex)

	mountJob, err := utils.RenderMountJob(pod.Name, pvc.Name, pvc.Spec.VolumeName, pvc.Namespace, nodeName, pv.Spec.CSI.FSType, mountpoint, containerIDs, preMountCmd, volumeMeta, metav1.OwnerReference{
		APIVersion: parentPVC.APIVersion,
		Kind:       parentPVC.Kind,
		Name:       pvc.Name,
		UID:        pvc.UID,
	})
	if err != nil {
		logger.Error(err, "Unable to render mount job")
		return
	}

	logger.Info("Create mount Job...", "containers", containerIDs, "mountpoint", mountpoint)

	if err := r.Client.Create(ctx, mountJob); err != nil {
		metrics.NewError("Job", mountJob.Name, mountJob.Namespace, "Kube API", "create")

		logger.Error(err, "Failed to create mount job")

		if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Failed to create mount Job for %s", config.Name), err.Error(), pod, config); err != nil {
			metrics.NewError("Event", "", "", "Kube API", "create")

			logger.Error(err, "Failed to create event")
		}

		return
	}
}

//nolint:gocyclo // It is complex we know
func (r *PVCReconciler) resizePVC(config *discoblocksondatiov1.DiskConfig, pod *corev1.Pod, capacity resource.Quantity, pvc *corev1.PersistentVolumeClaim, nodeName string, logger logr.Logger) {
	logger.Info("Update PVC...", "capacity", capacity.AsApproximateFloat64())

	pvc.Spec.Resources.Requests[corev1.ResourceStorage] = capacity

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if err := r.Client.Update(ctx, pvc); err != nil {
		metrics.NewError("PersistentVolumeClaim", pvc.Name, pvc.Namespace, "Kube API", "get")

		logger.Error(err, "Failed to update PVC")

		if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Failed to update PVC for %s: %s", config.Name, pvc.Name), err.Error(), pod, pvc); err != nil {
			metrics.NewError("Event", "", "", "Kube API", "create")

			logger.Error(err, "Failed to create event")
		}

		return
	}
	metrics.NewPVCOperation(pvc.Name, pvc.Namespace, "resize", capacity.String())

	if _, ok := pvc.Labels["discoblocks-parent"]; !ok {
		logger.Info("First PVC is managed by CSI driver")

		if err := r.EventService.SendNormal(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("New capacity of %s: %s", pvc.Name, capacity.String()), "Operation finished: disk managed by CSI driver", pod, pvc); err != nil {
			metrics.NewError("Event", "", "", "Kube API", "create")

			logger.Error(err, "Failed to create event")
		}

		return
	}

	sc := storagev1.StorageClass{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: config.Spec.StorageClassName}, &sc); err != nil {
		metrics.NewError("StorageClass", config.Spec.StorageClassName, "", "Kube API", "get")

		logger.Error(err, "StorageClass not found", "sc_name", config.Spec.StorageClassName)

		if apierrors.IsNotFound(err) {
			if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("StorageClass not found for %s: %s", config.Name, config.Spec.StorageClassName), err.Error(), pod, config); err != nil {
				metrics.NewError("Event", "", "", "Kube API", "create")

				logger.Error(err, "Failed to create event")
			}

			return
		}

		logger.Error(err, "Unable to fetch StorageClass")

		if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Failed to fetch StorageClass for %s: %s", config.Name, config.Spec.StorageClassName), err.Error(), pod, config); err != nil {
			metrics.NewError("Event", "", "", "Kube API", "create")

			logger.Error(err, "Failed to create event")
		}

		return
	}
	logger = logger.WithValues("provisioner", sc.Provisioner)

	driver := drivers.GetDriver(sc.Provisioner)
	if driver == nil {
		metrics.NewError("CSI", sc.Provisioner, "", sc.Provisioner, "GetDriver")

		dmErr := errors.New("driver not found: " + sc.Provisioner)

		logger.Error(dmErr, "Driver not found")

		if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Failed to find driver for %s: %s", config.Name, sc.Provisioner), dmErr.Error(), pod, config); err != nil {
			metrics.NewError("Event", "", "", "Kube API", "create")

			logger.Error(err, "Failed to create event")
		}

		return
	}

	if isFsManaged, err := driver.IsFileSystemManaged(); err != nil {
		metrics.NewError("CSI", "", "", sc.Provisioner, "IsFileSystemManaged")

		logger.Error(err, "Failed to call driver", "method", "IsFileSystemManaged")

		if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Failed to call driver.IsFileSystemManaged %s: %s", config.Name, sc.Provisioner), err.Error(), pod, config); err != nil {
			metrics.NewError("Event", "", "", "Kube API", "create")

			logger.Error(err, "Failed to create event")
		}

		return
	} else if isFsManaged {
		logger.Info("Filesystem will resized by CSI driver")

		if err := r.EventService.SendNormal(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("New capacity of %s: %s", pvc.Name, capacity.String()), "Operation finished: disk resizing by CSI driver", pod, pvc); err != nil {
			metrics.NewError("Event", "", "", "Kube API", "create")

			logger.Error(err, "Failed to create event")
		}

		return
	}

	waitForMeta, err := driver.WaitForVolumeAttachmentMeta()
	if err != nil {
		metrics.NewError("CSI", "", "", sc.Provisioner, "WaitForVolumeAttachmentMeta")

		logger.Error(err, "Failed to call driver", "method", "WaitForVolumeAttachmentMeta")

		if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Failed to call driver.WaitForVolumeAttachmentMeta %s: %s", config.Name, sc.Provisioner), err.Error(), pod, config); err != nil {
			metrics.NewError("Event", "", "", "Kube API", "create")

			logger.Error(err, "Failed to create event")
		}

		return
	}

	logger.Info("Find PersistentVolume...")

	pv := &corev1.PersistentVolume{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: pvc.Spec.VolumeName}, pv); err != nil {
		metrics.NewError("PersistentVolume", pvc.Spec.VolumeName, "", "Kube API", "get")

		logger.Error(err, "Failed to find PersistentVolume")

		if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Failed to fetch PV for %s: %s", config.Name, pv.Name), err.Error(), pod, config); err != nil {
			metrics.NewError("Event", "", "", "Kube API", "create")

			logger.Error(err, "Failed to create event")
		}

		return
	} else if pv.Spec.CSI == nil {
		metrics.NewError("PersistentVolume", pv.Name, "", "Kube API", "get")

		logger.Error(err, "Failed to find pv.spec.csi")

		if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Failed to find pv.spec.csi for %s: %s", config.Name, pv.Name), err.Error(), pod, pv); err != nil {
			metrics.NewError("Event", "", "", "Kube API", "create")

			logger.Error(err, "Failed to create event")
		}

		return
	}

	var volumeAttachment *storagev1.VolumeAttachment

	volumeMeta := ""
	if waitForMeta != "" {
		logger.Info("Fetch VolumeAttachment...")

		volumeAttachment, err = r.getVolumeAttachment(ctx, pvc.Spec.VolumeName)
		if err != nil {
			metrics.NewError("VolumeAttachment", "", "", "Kube API", "list")

			logger.Error(err, "Failed to fetch VolumeAttachment")

			if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Failed to fetch VolumeAttachment for %s: %s", config.Name, pv.Name), err.Error(), pod, pv); err != nil {
				metrics.NewError("Event", "", "", "Kube API", "create")

				logger.Error(err, "Failed to create event")
			}

			return
		}

		volumeMeta = volumeAttachment.Status.AttachmentMetadata[waitForMeta]
		if volumeMeta == "" {
			metrics.NewError("VolumeAttachment", volumeAttachment.Name, "", "Kube API", "get")

			logger.Error(err, "Failed to find VolumeAttachment meta")

			if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Failed to find VolumeAttachment meta for %s: %s", config.Name, pv.Name), err.Error(), pod, pv); err != nil {
				metrics.NewError("Event", "", "", "Kube API", "create")

				logger.Error(err, "Failed to create event")
			}

			return
		}
	}

	preResizeCmd, err := driver.GetPreResizeCommand(pv, volumeAttachment)
	if err != nil {
		metrics.NewError("CSI", pv.Name, "", sc.Provisioner, "GetPreResizeCommand")

		logger.Error(err, "Failed to call driver", "method", "GetPreResizeCommand")

		if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Failed to call driver.GetPreResizeCommand for %s: %s", config.Name, sc.Provisioner), err.Error(), pod, config); err != nil {
			metrics.NewError("Event", "", "", "Kube API", "create")

			logger.Error(err, "Failed to create event")
		}

		return
	}

	resizeJob, err := utils.RenderResizeJob(pod.Name, pvc.Name, pvc.Spec.VolumeName, pvc.Namespace, nodeName, pv.Spec.CSI.FSType, preResizeCmd, volumeMeta, metav1.OwnerReference{
		APIVersion: pvc.APIVersion,
		Kind:       pvc.Kind,
		Name:       pvc.Name,
		UID:        pvc.UID,
	})
	if err != nil {
		logger.Error(err, "Unable to render mount job")
		return
	} else if resizeJob == nil {
		return
	}

	logger.Info("Create resize Job...")

	if err := r.Client.Create(ctx, resizeJob); err != nil {
		metrics.NewError("Job", resizeJob.Name, resizeJob.Namespace, "Kube API", "create")

		logger.Error(err, "Failed to create resize job")

		if err := r.EventService.SendWarning(pod.Namespace, "Discoblocks", "PVC Monitor", fmt.Sprintf("Failed to create resize Job for %s", config.Name), err.Error(), pod, config); err != nil {
			metrics.NewError("Event", "", "", "Kube API", "create")

			logger.Error(err, "Failed to create event")
		}

		return
	}
}

func (r *PVCReconciler) getVolumeAttachment(ctx context.Context, volumeName string) (*storagev1.VolumeAttachment, error) {
	volumeAttachments := &storagev1.VolumeAttachmentList{}
	if err := r.Client.List(ctx, volumeAttachments, &client.ListOptions{
		FieldSelector: client.MatchingFieldsSelector{
			Selector: fields.OneTermEqualSelector("spec.source.persistentVolumeName", volumeName),
		},
	}); err != nil {
		return nil, err
	}

	switch {
	case len(volumeAttachments.Items) == 0:
		return nil, errors.New("failed to find VolumeAttachment")
	case len(volumeAttachments.Items) > 1:
		return nil, errors.New("more than one VolumeAttachment attached to PersistentVolume")
	}

	sort.Slice(volumeAttachments.Items, func(i, j int) bool {
		return volumeAttachments.Items[i].CreationTimestamp.UnixNano() < volumeAttachments.Items[j].CreationTimestamp.UnixNano()
	})

	return &volumeAttachments.Items[0], nil
}

type pvcEventFilter struct {
	logger logr.Logger
}

func (ef pvcEventFilter) Create(e event.CreateEvent) bool {
	newObj, ok := e.Object.(*corev1.PersistentVolumeClaim)
	if !ok {
		ef.logger.Error(errors.New("unsupported type"), "Unable to cast new object")
		return false
	}

	return controllerutil.ContainsFinalizer(newObj, utils.RenderFinalizer(newObj.Labels["discoblocks"]))
}

func (ef pvcEventFilter) Delete(_ event.DeleteEvent) bool {
	return false
}

func (ef pvcEventFilter) Update(e event.UpdateEvent) bool {
	newObj, ok := e.ObjectNew.(*corev1.PersistentVolumeClaim)
	if !ok {
		ef.logger.Error(errors.New("unsupported type"), "Unable to cast new object")
		return false
	}

	if !controllerutil.ContainsFinalizer(newObj, utils.RenderFinalizer(newObj.Labels["discoblocks"])) {
		return false
	}

	oldObj, ok := e.ObjectOld.(*corev1.PersistentVolumeClaim)
	if !ok {
		ef.logger.Error(errors.New("unsupported type"), "Unable to cast old object")
		return false
	}

	return oldObj.Status.Phase != newObj.Status.Phase || newObj.DeletionTimestamp != nil
}

func (ef pvcEventFilter) Generic(_ event.GenericEvent) bool {
	return false
}

// SetupWithManager sets up the controller with the Manager.
func (r *PVCReconciler) SetupWithManager(mgr ctrl.Manager) (chan<- bool, error) {
	closeChan := make(chan bool)

	go func() {
		ticker := time.NewTicker(monitoringPeriod)
		defer ticker.Stop()

		for {
			select {
			case <-closeChan:
				return
			case <-ticker.C:
				r.MonitorVolumes()
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if err := mgr.GetFieldIndexer().IndexField(ctx, &storagev1.VolumeAttachment{}, "spec.source.persistentVolumeName", func(rawObj client.Object) []string {
		va, ok := rawObj.(*storagev1.VolumeAttachment)
		if !ok {
			return nil
		}

		if va.Spec.Source.PersistentVolumeName == nil {
			return nil
		}

		return []string{*va.Spec.Source.PersistentVolumeName}
	}); err != nil {
		return nil, err
	}

	return closeChan, ctrl.NewControllerManagedBy(mgr).
		For(&corev1.PersistentVolumeClaim{}).
		WithEventFilter(pvcEventFilter{logger: mgr.GetLogger().WithName("PVCReconciler")}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		Complete(r)
}
