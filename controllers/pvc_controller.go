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
	"github.com/ondat/discoblocks/pkg/drivers"
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
	NodeCache  nodeCache
	InProgress sync.Map
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
	err := r.Get(ctx, req.NamespacedName, &pvc)
	switch {
	case err != nil && apierrors.IsNotFound(err):
		logger.Info("PVC not found")

		return ctrl.Result{}, nil
	case err != nil:
		return ctrl.Result{}, fmt.Errorf("unable to fetch PVC: %w", err)
	}

	logger.Info("Fetch DiskConfig...")

	config := discoblocksondatiov1.DiskConfig{}
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: pvc.Namespace, Name: pvc.Labels["discoblocks"]}, &config); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("PVC not found")

			return ctrl.Result{}, nil
		}

		logger.Info("Unable to fetch PVC", "error", err.Error())
		return ctrl.Result{}, errors.New("unable to fetch PVC")
	}
	logger = logger.WithValues("dc_name", config.Name)

	if pvc.DeletionTimestamp != nil {
		if _, ok := config.Status.PersistentVolumeClaims[pvc.Name]; ok {
			logger.Info("Remove status")
			delete(config.Status.PersistentVolumeClaims, pvc.Name)
		}
	} else {
		if config.Status.PersistentVolumeClaims == nil {
			config.Status.PersistentVolumeClaims = map[string]corev1.PersistentVolumeClaimPhase{}
		}

		logger.Info("Add status", "phase", pvc.Status.Phase)
		config.Status.PersistentVolumeClaims[pvc.Name] = pvc.Status.Phase
	}

	logger.Info("Update DiskConfig status...")

	if err := r.Client.Status().Update(ctx, &config); err != nil {
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
			logger.Error(err, "Unable to fetch Pods")
			continue
		}

		sem := utils.CreateSemaphore(concurrency, config.Spec.Policy.CoolDown.Duration)
		wg := sync.WaitGroup{}

		for p := range pods.Items {
			pod := pods.Items[p]

			if pod.DeletionTimestamp != nil {
				continue
			}

			wg.Add(1)

			go func() {
				defer wg.Done()

				unlock, err := utils.WaitForSemaphore(ctx, sem)
				if err != nil {
					logger.Info("Context deadline")
					return
				}
				defer unlock()

				logger := logger.WithValues("pod_name", pod.Name)

				logger.Info("Fetch DiskInfo...")

				diskInfo, err := utils.FetchDiskInfo(fmt.Sprintf("%s:9100", pod.Status.PodIP))
				if err != nil {
					logger.Error(err, "Unable to fetch disk info")
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
							logger.Error(err, "Unable to convert index")
							continue
						}
					}

					lastMountPoint := utils.RenderMountPoint(config.Spec.MountPointPattern, config.Name, actIndex)

					logger = logger.WithValues("last_pvc", lastPVC.Name, "last_pv", lastPVC.Spec.VolumeName, "last_mp", lastMountPoint)

					lastUsed, ok := diskInfo[lastMountPoint]
					if !ok {
						logger.Error(err, "Unable to find metrics", "disk_info", diskInfo)
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
						logger.Error(errors.New("node not found: "+pod.Status.HostIP), "Node not found", "IP", pod.Status.HostIP)
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

					go r.resizePVC(&config, newCapacity, lastPVC, nodeName, logger)
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
			logger.Error(err, "StorageClass not found", "sc_name", config.Spec.StorageClassName)
			return
		}
		logger.Error(err, "Unable to fetch StorageClass")
		return
	}
	logger = logger.WithValues("provisioner", sc.Provisioner)

	logger.Info("Fetch Node...")

	node := &corev1.Node{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: nodeName}, node); err != nil {
		logger.Error(err, "Failed to get Node")
		return
	}

	driver := drivers.GetDriver(sc.Provisioner)
	if driver == nil {
		logger.Error(errors.New("driver not found: "+sc.Provisioner), "Driver not found")
		return
	}

	prefix := utils.GetNamePrefix(discoblocksondatiov1.ReadWriteOnce, string(config.UID), nodeName)

	pvc, err := utils.NewPVC(config, prefix, driver)
	if err != nil {
		logger.Error(err, "Unable to construct new PVC")
		return
	}
	logger = logger.WithValues("pvc_name", pvc.Name)

	scAllowedTopology, err := driver.GetStorageClassAllowedTopology(node)
	if err != nil {
		logger.Error(err, "Failed to call driver", "method", "GetStorageClassAllowedTopology")
		return
	}

	waitForMeta, err := driver.WaitForVolumeAttachmentMeta()
	if err != nil {
		logger.Error(err, "Failed to call driver", "method", "WaitForVolumeAttachmentMeta")
		return
	}

	if len(scAllowedTopology) != 0 {
		topologySC, err := utils.NewStorageClass(&sc, scAllowedTopology)
		if err != nil {
			logger.Error(err, "Failed to render get NewStorageClass")
			return
		}

		logger.Info("Create StorageClass...")

		if err = r.Create(ctx, topologySC); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				logger.Error(err, "Failed to create StorageClass")
				return
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

	if err = r.Create(ctx, pvc); err != nil {
		logger.Error(err, "Failed to create PVC")
		return
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), config.Spec.Policy.CoolDown.Duration)
	defer cancel()

	logger.Info("Wait PersistentVolume...")

	var waitPVErr error
WAIT_PV:
	for {
		select {
		case <-waitCtx.Done():
			if waitPVErr == nil {
				waitPVErr = waitCtx.Err()
			}
			logger.Error(waitPVErr, "PV creation wait timeout")
			return
		default:
			if waitPVErr = r.Get(ctx, types.NamespacedName{Namespace: pvc.Namespace, Name: pvc.Name}, pvc); waitPVErr == nil &&
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
			if waitCSIErr == nil {
				waitCSIErr = waitCtx.Err()
			}
			logger.Error(waitCSIErr, "CSI provision wait timeout")
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

	if err = r.Create(ctx, volumeAttachment); err != nil {
		logger.Error(err, "Failed to create volume attachment")
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
				if waitVAErr == nil {
					waitVAErr = waitCtx.Err()
				}
				logger.Error(waitVAErr, "VolumeAttachment wait timeout")
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
		logger.Error(err, "Failed to call GetPreMountCommand")
		return
	}

	mountpoint := utils.RenderMountPoint(config.Spec.MountPointPattern, config.Name, nextIndex)

	mountJob, err := utils.RenderMountJob(pvc.Name, pvc.Spec.VolumeName, pvc.Namespace, nodeName, pv.Spec.CSI.FSType, mountpoint, containerIDs, preMountCmd, volumeMeta, metav1.OwnerReference{
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

	if err := r.Create(ctx, mountJob); err != nil {
		logger.Error(err, "Failed to create mount job")
		return
	}
}

func (r *PVCReconciler) resizePVC(config *discoblocksondatiov1.DiskConfig, capacity resource.Quantity, pvc *corev1.PersistentVolumeClaim, nodeName string, logger logr.Logger) {
	logger.Info("Update PVC...", "capacity", capacity.AsApproximateFloat64())

	pvc.Spec.Resources.Requests[corev1.ResourceStorage] = capacity

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if err := r.Update(ctx, pvc); err != nil {
		logger.Error(err, "Failed to update PVC")
		return
	}

	if _, ok := pvc.Labels["discoblocks-parent"]; !ok {
		logger.Info("First PVC is managed by CSI driver")
		return
	}

	sc := storagev1.StorageClass{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: config.Spec.StorageClassName}, &sc); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Error(err, "StorageClass not found", "sc_name", config.Spec.StorageClassName)
			return
		}
		logger.Error(err, "Unable to fetch StorageClass")
		return
	}
	logger = logger.WithValues("provisioner", sc.Provisioner)

	driver := drivers.GetDriver(sc.Provisioner)
	if driver == nil {
		logger.Error(errors.New("driver not found: "+sc.Provisioner), "Driver not found")
		return
	}

	if isFsManaged, err := driver.IsFileSystemManaged(); err != nil {
		logger.Error(err, "Failed to call IsFileSystemManaged")
		return
	} else if isFsManaged {
		logger.Info("Filesystem will resized by CSI driver")
		return
	}

	waitForMeta, err := driver.WaitForVolumeAttachmentMeta()
	if err != nil {
		logger.Error(err, "Failed to call driver", "method", "WaitForVolumeAttachmentMeta")
		return
	}

	logger.Info("Find PersistentVolume...")

	pv := &corev1.PersistentVolume{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: pvc.Spec.VolumeName}, pv); err != nil {
		logger.Error(err, "Failed to find PersistentVolume")
		return
	} else if pv.Spec.CSI == nil {
		logger.Error(err, "Failed to find pv.spec.csi")
		return
	}

	var volumeAttachment *storagev1.VolumeAttachment

	volumeMeta := ""
	if waitForMeta != "" {
		logger.Info("Fetch VolumeAttachment...")

		volumeAttachment, err = r.getVolumeAttachment(ctx, pvc.Spec.VolumeName)
		if err != nil {
			logger.Error(err, "Failed to fetch VolumeAttachment")
			return
		}

		volumeMeta = volumeAttachment.Status.AttachmentMetadata[waitForMeta]
		if volumeMeta == "" {
			logger.Error(err, "Failed to find VolumeAttachment meta")
			return
		}
	}

	preResizeCmd, err := driver.GetPreResizeCommand(pv, volumeAttachment)
	if err != nil {
		logger.Error(err, "Failed to call GetPreResizeCommand")
		return
	}

	resizeJob, err := utils.RenderResizeJob(pvc.Name, pvc.Spec.VolumeName, pvc.Namespace, nodeName, pv.Spec.CSI.FSType, preResizeCmd, volumeMeta, metav1.OwnerReference{
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

	if err := r.Create(ctx, resizeJob); err != nil {
		logger.Error(err, "Failed to create resize job")
		return
	}
}

func (r *PVCReconciler) getVolumeAttachment(ctx context.Context, volumeName string) (*storagev1.VolumeAttachment, error) {
	volumeAttachments := &storagev1.VolumeAttachmentList{}
	if err := r.List(ctx, volumeAttachments, &client.ListOptions{
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

	return oldObj.DeletionTimestamp != nil ||
		newObj.DeletionTimestamp != nil ||
		oldObj.Status.Phase != newObj.Status.Phase
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
