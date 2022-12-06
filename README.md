# Discoblocks

<p align="center">
    <img src="https://github.com/ondat/discoblocks/blob/main/assets/DiscoBlocks-2.png" width="25%" height="25%" >
</p>
<p align="center">
  <a href="https://github.com/ondat/discoblocks/actions/workflows/e2e-on-pr.yml">
    <img alt="end-2-end build" src="https://github.com/ondat/discoblocks/actions/workflows/e2e-on-pr.yml/badge.svg"/>
  </a>
  <a href="https://goreportcard.com/report/github.com/ondat/discoblocks">
    <img alt="scorecards supply-chain security" src="https://goreportcard.com/badge/github.com/ondat/discoblocks"/>
  </a>
  <a href="https://github.com/ondat/discoblocks/actions/workflows/scorecards-analysis.yml">
    <img alt="scorecards supply-chain security" src="https://github.com/ondat/discoblocks/actions/workflows/scorecards-analysis.yml/badge.svg"/>
  </a>
  <a href="https://bestpractices.coreinfrastructure.org/projects/6047">
    <img src="https://bestpractices.coreinfrastructure.org/projects/6047/badge">
  </a>
</p>

The [end-2-end build](https://github.com/ondat/discoblocks/blob/main/.github/workflows/e2e-on-pr.yml) includes:

- [gosec scanning](https://github.com/ondat/discoblocks/blob/main/.github/workflows/_gosecscan.yml)
- [golang-ci linting](https://github.com/ondat/discoblocks/blob/main/.github/workflows/_gocilint.yml)
- [Docker image build](https://github.com/ondat/discoblocks/blob/main/.github/workflows/_docker-build.yml)
- [Trivy vulnerability scanning](https://github.com/ondat/discoblocks/blob/main/.github/workflows/_trivy.yml)

-----

**Please note**: We take security and users' trust seriously. If you believe you have found a security issue in Discoblocks, *please responsibly disclose* by following the [security policy](https://github.com/ondat/discoblocks/security/policy).

-----

This is the home of [Discoblocks](https://discoblocks.io), an open-source declarative disk configuration system for Kubernetes helping to automate CRUD (Create, Read, Update, Delete) operations for cloud disk device resources attached to Kubernetes cluster nodes.

- Website: [https://discoblocks.io](https://discoblocks.io)
- Announcement & Forum: [GitHub Discussions](https://github.com/ondat/discoblocks/discussions)
- Documentation: [GitHub Wiki](https://github.com/ondat/discoblocks/wiki)
- Recording of a demo: [Demo](https://user-images.githubusercontent.com/55788733/168624989-c9b1d469-d3e5-40e7-8858-c9ff4a5b5428.mp4)

## About the name

Some call storage snorage because they believe it is boring... but we could have fun and dance with the block devices!

## Why discoblocks

Discoblocks can be leveraged by cloud native data management platform (like [Ondat.io](https://ondat.io)) to management the backend disks in the cloud.

When using such data management platform to overcome the block disk device limitation from hyperscalers, a new set of manual operational tasks needs to be considered like:

- provisioning block devices on the Kubernetes worker nodes 
- partioning, formating, mounting the block devices within specific path (like /var/lib/vendor)
- capacity management and monitoring
- resizing and optimizing layouts related to capacity management
- decommissioning the devices in secure way
  - by default every resource created by DiscoBlocks has a finalizer, so deletion will be blocked until the corresponding DiskConfig has been deleted
  - by default every additional disk has owner reference to the first disk ever created for pod, Deletion of first PVC terminates all other

At the current stage, Discoblocks is leveraging the available hyperscaler CSI (Container Storage Interface) within the Kubernetes cluster to:

- introduce a CRD (Custom Resource Definition) per workload with
  - StorageClass name
  - capacity
  - mount path within the Pod
  - nodeSelector
  - podSelector
  - access modes: Access mode of PersistentVolume
  - availability mode:
    - ReadWriteOnce: New disk for each pod, including pod restart
    - ReadWriteSame: All pod gets the same volume on the same node
    - ReadWriteDaemon: DaemonSet pods always re-use existing volume on the same node
  - upscale policy
    - upscale trigger percentage
    - maximum capacity of disk
    - maximum number of disks per pod
    - extend capacity
    - cool down period after upscale
    - pause autoscaling
- provision the relevant disk device using the CSI (like EBS on AWS) when the workload deployment will happen
- monitor the volume(s)
- resize automatically the volume based on the upscale policy
- create and mount new volumes in running pod based on maximum number of disks

**Note:** that an application could be using Discoblocks to get persistent storage but this option would not be safe for production as there will not be any data platform management to address high availability, replication, fencing, encryption, ...

## Demo from our core Developer

https://user-images.githubusercontent.com/55788733/168624989-c9b1d469-d3e5-40e7-8858-c9ff4a5b5428.mp4

## How to install

### Prerequisites

- Kubernetes cluster
- Kubernetes CLI
- Cert Manager
  - `kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.8.0/cert-manager.yaml`

### Install on AWS with EBS backend

```bash
cat <<EOF | kubectl apply -f -
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: ebs-sc
provisioner: ebs.csi.aws.com
parameters:
  type: gp3
allowVolumeExpansion: true
reclaimPolicy: Retain
volumeBindingMode: WaitForFirstConsumer
EOF

kubectl apply -f https://github.com/ondat/discoblocks/releases/download/v[VERSION]/discoblocks-bundle.yaml

cat <<EOF | kubectl apply -f -
apiVersion: discoblocks.ondat.io/v1
kind: DiskConfig
metadata:
  name: nginx
spec:
  storageClassName: ebs-sc
  capacity: 1Gi
  mountPointPattern: /usr/share/nginx/html/data
  nodeSelector:
    matchLabels:
      kubernetes.io/os: linux 
  podSelector:
       app: nginx
  policy:
    upscaleTriggerPercentage: 80
    maximumCapacityOfDisk: 2Gi
    maximumNumberOfDisks: 3
    coolDown: 10m
EOF

kubectl apply create deployment --image=nginx nginx
```

## FAQ

- How to find logs?
  - `kubectl logs -n kube-system deploy/discoblocks-controller-manager`
- Which PersistentVolumeClaims are created by Discoblock?
  - `kubectl get diskconfig [DISK_CONFIG_NAME] -o yaml | grep "    message: "`
  - `kubectl get pvc -l discoblocks=[DISK_CONFIG_NAME]`
- How to find first volume of a PersistentVolumeClaim groups?
  - `kubectl get pvc -l 'discoblocks=[DISK_CONFIG_NAME],!discoblocks-parent'`
- How to find additional volumes of a PersistentVolumeClaim groups?
  - `kubectl get pvc -l 'discoblocks=[DISK_CONFIG_NAME],discoblocks-parent=[PVC_NAME]'`
- How to delete a group of PersistentVolumeClaims?
  - You have to delete only the first volume, all other members of the group would be terminated by Kbernetes.
- What Discoblocks related events happened on my Pod?
  - `kubectl get event --field-selector involvedObject.name=[POD_NAME] -o wide | grep discoblocks.ondat.io`
- Why my deleted objects are hanging in `Terminating` state?
  - Discoblocks prevents accidentally deletion with finalizers on almost every object it touches.
  - `DiskConfig` object deletion removes all finalizers.
  - `kubectl patch pvc [PVC_NAME] --type=json -p='[{"op": "remove", "path": "/metadata/finalizers/0"}]'`
- How to ensure volume monitoring works in my Pod?
  - `kubectl debug [POD_NAME] -q -c debug --image=nixery.dev/shell/curl -- sleep infinity && kubectl exec [POD_NAME] -c debug -- curl -s telnet://localhost:9100`
- How to enable Prometheus integration?
  - `kubectl apply -f https://raw.githubusercontent.com/ondat/discoblocks/v[VERSION]/config/prometheus/monitor.yaml`

## Monitoring, metrics

Prometheus integration is isabled by default, to enable it please apply the [ServiceMonitor](https://raw.githubusercontent.com/ondat/discoblocks/main/config/prometheus/monitor.yaml) manifest on your cluster.

Metrics provided by Discoblocks:

- Golang related metrics
- PersistentVolumeClaim operations by type: `discoblocks_pvc_operation_counter`
  - resourceName
  - resourceNamespace
  - operation
  - size
- Errors by type: `discoblocks_error_counter`
  - resourceType
  - resourceName
  - resourceNamespace
  - errorType
  - operation

## Contributing Guidelines

We love your input! We want to make contributing to this project as easy and transparent as possible. You can find the full guidelines [here](https://github.com/ondat/discoblocks/blob/main/CONTRIBUTING.md).

## Community

Please reach out for any questions or issues via our [Github Discussions](https://github.com/ondat/discoblocks/discussions).

Alternatively you can:

- Raise an [issue](https://github.com/ondat/discoblocks/issues/new/choose) or PR on this repo
- Follow us on Twitter [@ondat_io](https://twitter.com/ondat_io)

## Roadmap

[Project Kanban board](https://github.com/orgs/ondat/projects/2/views/2?layout=board)

## License

Discoblocks is under the Apache 2.0 license. See [LICENSE](https://github.com/ondat/discoblocks/blob/main/LICENSE) file for details.
