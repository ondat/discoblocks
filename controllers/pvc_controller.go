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
	"io"
	"net/http"
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

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute-time.Second)
	defer cancel()

	label, err := labels.NewRequirement("discoblocks", selection.Exists, nil)
	if err != nil {
		logger.Error(err, "Unable to parse Service label selector")
		return
	}
	endpointSelector := labels.NewSelector().Add(*label)

	logger.Info("Fetch Endpoints...")

	endpoints := corev1.EndpointsList{}
	if err := r.Client.List(ctx, &endpoints, &client.ListOptions{
		LabelSelector: endpointSelector,
	}); err != nil {
		logger.Error(err, "Unable to fetch Services")
		return
	}

	diskConfigCache := map[types.NamespacedName]struct {
		config discoblocksondatiov1.DiskConfig
		pvcs   []*corev1.PersistentVolumeClaim
	}{}

	discoblocks := map[types.NamespacedName][]string{}
	metrics := map[types.NamespacedName][]string{}
	for i := range endpoints.Items {
		if endpoints.Items[i].DeletionTimestamp != nil {
			continue
		}

		for _, ss := range endpoints.Items[i].Subsets {
			for _, ip := range ss.Addresses {
				podName := types.NamespacedName{Namespace: ip.TargetRef.Namespace, Name: ip.TargetRef.Name}

				if _, ok := discoblocks[podName]; !ok {
					discoblocks[podName] = []string{}
				}
				for k, v := range endpoints.Items[i].Labels {
					if strings.HasPrefix(k, "discoblocks/") {
						discoblocks[podName] = append(discoblocks[podName], v)
					}
				}

				logger := logger.WithValues("pod_name", podName.String(), "ep_name", endpoints.Items[i].Name, "IP", ip.IP)

				req, err := http.NewRequest("GET", fmt.Sprintf("http://%s:9100/metrics", ip.IP), http.NoBody)
				if err != nil {
					logger.Error(err, "Request error")
					continue
				}

				const div = 4
				callCtx, cancel := context.WithTimeout(ctx, time.Minute/div)

				logger.Info("Call Endpoint...")

				resp, err := http.DefaultClient.Do(req.WithContext(callCtx))
				if err != nil {
					cancel()
					logger.Error(err, "Connection error")
					continue
				}
				cancel()

				rawBody, err := io.ReadAll(resp.Body)
				if err != nil {
					logger.Error(err, "Body read error")
					continue
				}
				if err = resp.Body.Close(); err != nil {
					logger.Error(err, "Body close error")
					continue
				}

				for _, line := range strings.Split(string(rawBody), "\n") {
					if !strings.HasPrefix(line, "node_filesystem_avail_bytes") {
						continue
					}

					mf, err := utils.ParsePrometheusMetric(line)
					if err != nil {
						logger.Error(err, "Failed to parse metrics")
						continue
					}

					if _, ok := mf["node_filesystem_avail_bytes"]; !ok {
						logger.Error(err, "Failed to find node_filesystem_avail_bytes", "metric", line)
						continue
					}

					if _, ok := metrics[podName]; !ok {
						metrics[podName] = []string{}
					}
					metrics[podName] = append(metrics[podName], line)
				}
			}
		}
	}

	if len(metrics) == 0 {
		logger.Info("Metrics data not found")
		return
	}

	for podName, diskConfigNames := range discoblocks {
		logger := logger.WithValues("pod_name", podName.String())

		logger.Info("Fetch Pod...")

		pod := corev1.Pod{}
		if err := r.Client.Get(ctx, podName, &pod); err != nil {
			logger.Error(err, "Failed to fetch pod error")
			continue
		}

		for _, diskConfigName := range diskConfigNames {
			diskConfigName := types.NamespacedName{Namespace: pod.Namespace, Name: diskConfigName}

			logger := logger.WithValues("dc_name", diskConfigName.String())

			cached, ok := diskConfigCache[diskConfigName]
			if !ok {
				logger.Info("Fetch DiskConfig...")

				cached = struct {
					config discoblocksondatiov1.DiskConfig
					pvcs   []*corev1.PersistentVolumeClaim
				}{
					config: discoblocksondatiov1.DiskConfig{},
				}
				if err := r.Client.Get(ctx, diskConfigName, &cached.config); err != nil {
					logger.Error(err, "Failed to fetch DiskConfig error")
					continue
				}
				diskConfigCache[diskConfigName] = cached
			}

			if cached.config.Spec.Policy.Pause {
				logger.Info("Autoscaling paused")
				continue
			}

			last, loaded := r.InProgress.Load(cached.config.Name)
			if loaded && last.(time.Time).Add(cached.config.Spec.Policy.CoolDown.Duration).After(time.Now()) {
				logger.Info("Autoscaling cooldown")
				continue
			}

			label, err := labels.NewRequirement("discoblocks", selection.Equals, []string{cached.config.Name})
			if err != nil {
				logger.Error(err, "Unable to parse PVC label selector")
				continue
			}
			pvcSelector := labels.NewSelector().Add(*label)

			logger.Info("Fetch PVCs...")

			if cached.pvcs == nil {
				pvcs := corev1.PersistentVolumeClaimList{}
				if err = r.Client.List(ctx, &pvcs, &client.ListOptions{
					Namespace:     cached.config.Namespace,
					LabelSelector: pvcSelector,
				}); err != nil {
					logger.Error(err, "Unable to fetch PVCs")
					continue
				}

				cached.pvcs = []*corev1.PersistentVolumeClaim{}
				for i := range pvcs.Items {
					if pvcs.Items[i].DeletionTimestamp != nil ||
						!controllerutil.ContainsFinalizer(&pvcs.Items[i], utils.RenderFinalizer(cached.config.Name)) ||
						pvcs.Items[i].Status.ResizeStatus != nil && *pvcs.Items[i].Status.ResizeStatus != corev1.PersistentVolumeClaimNoExpansionInProgress {
						continue
					}

					cached.pvcs = append(cached.pvcs, &pvcs.Items[i])

					logger.Info("Volume found", "pvc_name", pvcs.Items[i].Name)
				}
			}

			if len(cached.pvcs) == 0 {
				logger.Error(err, "Unable to find any PVC")
				continue
			}

			podPVCs := []*corev1.PersistentVolumeClaim{}
			for i := range pod.Spec.Volumes {
				for j := range cached.pvcs {
					if pod.Spec.Volumes[i].PersistentVolumeClaim != nil && pod.Spec.Volumes[i].PersistentVolumeClaim.ClaimName == cached.pvcs[j].Name {
						podPVCs = append(podPVCs, cached.pvcs[j])
					}
				}
			}

			if len(podPVCs) == 0 {
				logger.Error(err, "Unable to find any PVC for Pod")
				continue
			}

			sort.Slice(podPVCs, func(i, j int) bool {
				return podPVCs[i].CreationTimestamp.UnixNano() < podPVCs[j].CreationTimestamp.UnixNano()
			})

			const hundred = 100

			lastPVC := podPVCs[len(podPVCs)-1]
			actualCapacity := lastPVC.Spec.Resources.Requests[corev1.ResourceStorage]
			treshold := actualCapacity.AsApproximateFloat64() * float64(cached.config.Spec.Policy.UpscaleTriggerPercentage) / hundred

			lastDiskMetrics := ""
			for _, metric := range metrics[podName] {
				mf, err := utils.ParsePrometheusMetric(metric)
				if err != nil {
					logger.Error(err, "Failed to parse metrics")
					continue
				}

			FIND_METRICS:
				for _, m := range mf["node_filesystem_avail_bytes"].Metric {
					for _, l := range m.Label {
						if l.Name == nil || l.Value == nil || *l.Name != "mountpoint" ||
							(!strings.HasSuffix(*l.Value, fmt.Sprintf("pv/%s/globalmount", lastPVC.Spec.VolumeName)) &&
								utils.GetMountPointIndex(cached.config.Spec.MountPointPattern, cached.config.Name, *l.Value) < 0) {
							continue
						}

						lastDiskMetrics = metric

						break FIND_METRICS
					}
				}
			}

			if lastDiskMetrics == "" {
				logger.Error(err, "Unable to find metrics")
				continue
			}

			logger.Info("Last PVC metric", "metric", lastDiskMetrics)

			available, err := utils.ParsePrometheusMetricValue(lastDiskMetrics)
			if err != nil {
				logger.Error(err, "Metric is invalid")
				continue
			}

			logger.Info("Capacities", "actual", fmt.Sprintf("%.2f", actualCapacity.AsApproximateFloat64()), "available", fmt.Sprintf("%.2f", available), "treshold", fmt.Sprintf("%.2f", treshold))

			if available > treshold {
				logger.Info("Disk size ok")
				continue
			}

			newCapacity := cached.config.Spec.Policy.ExtendCapacity
			newCapacity.Add(actualCapacity)

			logger = logger.WithValues("new_capacity", newCapacity.String(), "max_capacity", cached.config.Spec.Policy.MaximumCapacityOfDisk.String(), "no_disks", len(podPVCs), "max_disks", cached.config.Spec.Policy.MaximumNumberOfDisks)

			logger.Info("Find Node name")

			nodeName := r.NodeCache.GetNodesByIP()[pod.Status.HostIP]
			if nodeName == "" {
				logger.Error(errors.New("node not found: "+pod.Status.HostIP), "Node not found", "IP", pod.Status.HostIP)
				continue
			}

			logger = logger.WithValues("node_name", nodeName)

			if newCapacity.Cmp(cached.config.Spec.Policy.MaximumCapacityOfDisk) == 1 {
				if cached.config.Spec.Policy.MaximumNumberOfDisks > 0 && len(podPVCs) >= int(cached.config.Spec.Policy.MaximumNumberOfDisks) {
					logger.Info("Already maximum number of disks", "number", cached.config.Spec.Policy.MaximumNumberOfDisks)
					continue
				}

				logger.Info("New disk needed")

				nextIndex := 1
				if lastIndex, ok := lastPVC.Labels["discoblocks-index"]; ok {
					i, err := strconv.Atoi(lastIndex)
					if err != nil {
						logger.Error(err, "Unable to convert index")
						continue
					}
					nextIndex = i + 1
				}

				logger.Info("Next index", "index", nextIndex)

				containerIDs := []string{}
				for i := range pod.Status.ContainerStatuses {
					cID := pod.Status.ContainerStatuses[i].ContainerID
					for _, prefix := range []string{"containerd://", "docker://"} {
						cID = strings.TrimPrefix(cID, prefix)
					}

					containerIDs = append(containerIDs, cID)
				}

				r.InProgress.Store(cached.config.Name, time.Now())

				go r.createPVC(&cached.config, podPVCs[0], containerIDs, nodeName, pod.Spec.HostPID, nextIndex, logger)

				continue
			}

			logger.Info("Resize needed")

			r.InProgress.Store(cached.config.Name, time.Now())

			go r.resizePVC(&cached.config, newCapacity, lastPVC, nodeName, logger)
		}
	}
}

func (r *PVCReconciler) createPVC(config *discoblocksondatiov1.DiskConfig, parentPVC *corev1.PersistentVolumeClaim, containerIDs []string, nodeName string, hostPID bool, nextIndex int, logger logr.Logger) {
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

	owner := metav1.OwnerReference{
		APIVersion: parentPVC.APIVersion,
		Kind:       parentPVC.Kind,
		Name:       pvc.Name,
		UID:        pvc.UID,
	}

	attachJob, err := utils.RenderAttachJob(pvc.Name, pvc.Namespace, nodeName, owner)
	if err != nil {
		logger.Error(err, "Unable to render attach job")
		return
	}

	logger.Info("Create attach Job...")

	if err := r.Create(ctx, attachJob); err != nil {
		logger.Error(err, "Failed to create attach job")
		return
	}

	logger.Info("Wait PVC...")

	waitCtx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()

WAIT_PVC:
	for {
		select {
		case <-waitCtx.Done():
			logger.Error(waitCtx.Err(), "PVC creation wait timeout")
			return
		default:
			if err = r.Get(ctx, types.NamespacedName{Namespace: pvc.Namespace, Name: pvc.Name}, pvc); err == nil &&
				pvc.Spec.VolumeName != "" {
				break WAIT_PVC
			}

			<-time.NewTimer(time.Second).C
		}
	}

	waitForMeta, err := driver.WaitForVolumeAttachmentMeta()
	if err != nil {
		logger.Error(err, "Failed to call driver", "method", "WaitForVolumeAttachmentMeta")
		return
	}

	logger.Info("Wait VolumeAttachment...", "waitForMeta", waitForMeta)

	dev := ""
	if waitForMeta != "" {
		logger.Info("Wait VolumeAttachment...")

	WAIT_VA:
		for {
			select {
			case <-waitCtx.Done():
				logger.Error(waitCtx.Err(), "VolumeAttachment creation wait timeout")
				return
			default:
				va, err := r.getVolumeAttachment(ctx, pvc.Spec.VolumeName)
				if err != nil || !va.Status.Attached || va.Status.AttachmentMetadata[waitForMeta] == "" {
					<-time.NewTimer(time.Second).C
					continue
				}

				dev = va.Status.AttachmentMetadata[waitForMeta]

				break WAIT_VA
			}
		}
	}

	mountpoint := utils.RenderMountPoint(config.Spec.MountPointPattern, config.Name, nextIndex)

	devPath, err := driver.GetDevicePath()
	if err != nil {
		logger.Error(err, "Failed to call GetDevicePath")
		return
	}

	devLookupCmd, err := driver.GetDeviceLookupCommand()
	if err != nil {
		logger.Error(err, "Failed to call GetDeviceLookupCommand")
		return
	}

	fsManaged, err := driver.IsFileSystemManaged()
	if err != nil {
		logger.Error(err, "Failed to call IsFileSystemManaged")
		return
	}

	logger.Info("Find PersistentVolume...")

	pv, err := r.getPersistentVolume(ctx, pvc.Name)
	if err != nil {
		logger.Error(err, "Failed to find PersistentVolume")
		return
	} else if pv.Spec.CSI == nil {
		logger.Error(err, "Failed to find pv.spec.csi")
		return
	}

	mountJob, err := utils.RenderMountJob(pvc.Name, pvc.Namespace, nodeName, dev, devPath, pv.Spec.CSI.FSType, mountpoint, containerIDs, devLookupCmd, fsManaged, hostPID, owner)
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
		return
	}

	logger.Info("Resizing file-system...")

	waitForMeta, err := driver.WaitForVolumeAttachmentMeta()
	if err != nil {
		logger.Error(err, "Failed to call driver", "method", "WaitForVolumeAttachmentMeta")
		return
	}

	dev := ""
	if waitForMeta != "" {
		logger.Info("Fetch VolumeAttachment...")

		va, err := r.getVolumeAttachment(ctx, pvc.Spec.VolumeName)
		if err != nil {
			logger.Error(err, "Failed to fetch VolumeAttachment")
			return
		}

		dev = va.Status.AttachmentMetadata[waitForMeta]
		if dev == "" {
			logger.Error(err, "Failed to find VolumeAttachment meta")
			return
		}
	}

	logger.Info("Find PersistentVolume...")

	pv, err := r.getPersistentVolume(ctx, pvc.Name)
	if err != nil {
		logger.Error(err, "Failed to find PersistentVolume")
		return
	} else if pv.Spec.CSI == nil {
		logger.Error(err, "Failed to find pv.spec.csi")
		return
	}

	resizeJob, err := utils.RenderResizeJob(pvc.Name, pvc.Namespace, nodeName, dev, pv.Spec.CSI.FSType, metav1.OwnerReference{
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

func (r *PVCReconciler) getPersistentVolume(ctx context.Context, pvcName string) (*corev1.PersistentVolume, error) {
	pvList := corev1.PersistentVolumeList{}
	if err := r.List(ctx, &pvList, &client.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("spec.claimRef.name", pvcName),
	}); err != nil {
		return nil, fmt.Errorf("failed to list PVs: %w", err)
	}

	switch {
	case len(pvList.Items) == 0:
		return nil, errors.New("failed to find PersistentVolume")
	case len(pvList.Items) > 1:
		return nil, errors.New("more than one PersistentVolume attached to PersistentVolumeClaim")
	}

	return &pvList.Items[0], nil
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
		const two = 2
		ticker := time.NewTicker(time.Minute / two)
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

	if err := mgr.GetFieldIndexer().IndexField(ctx, &corev1.PersistentVolume{}, "spec.claimRef.name", func(rawObj client.Object) []string {
		pv, ok := rawObj.(*corev1.PersistentVolume)
		if !ok {
			return nil
		}

		if pv.Spec.ClaimRef == nil {
			return nil
		}

		return []string{pv.Spec.ClaimRef.Name}
	}); err != nil {
		return nil, err
	}

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
