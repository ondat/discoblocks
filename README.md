
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

* Website: https://discoblocks.io 
* Announcement & Forum: [GitHub Discussions](https://github.com/ondat/discoblocks/discussions)
* Documentation: [GitHub Wiki](https://github.com/ondat/discoblocks/wiki)
* Recording of a demo: 

## Why discoblocks

Discoblocks can be leveraged by cloud-native data management platforms (like [Ondat.io](https://ondat.io)) to manage the backend disks in the cloud.  

When using such a data management platform to overcome the block disk device limitation from hyperscalers, a new set of manual operational tasks needs to be considered like:
- provisioning block devices on the Kubernetes worker nodes 
- partitioning, formatting, and mounting the block devices within specific paths (like /var/lib/vendor) 
- capacity management and monitoring
- resizing and optimizing layouts related to capacity management
- decommissioning the devices in a secure way
  - by default every resource created by DiscoBlocks has a finalizer, so deletion will be blocked until the corresponding DiskConfig has been deleted
  - by default every additional disk has owner reference to the first disk ever created for the pod, Deletion of the first PVC terminates all other

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
    - maximum capacity of the disk
    - maximum number of disks per pod
    - extend capacity
    - cool down period after upscale
    - pause autoscaling
- provision the relevant disk device using the CSI (like EBS on AWS) when the workload deployment will happen
- monitor the volume(s)
- resize automatically the volume based on the upscale policy
- create and mount new volume based on the upscale policy

**Note:** that an application could be using Discoblocks to get persistent storage but this option would not be safe for production as there will not be any data platform management to address high availability, replication, fencing, and encryption, ...

## Demo from our core Developer

https://user-images.githubusercontent.com/55788733/168624989-c9b1d469-d3e5-40e7-8858-c9ff4a5b5428.mp4

## FAQ
 * Why this name?
  Some call storage snorage because they believe it is boring... but we could have fun and dance with the block devices!
 * How Discoblocks monitors volumes?
  Discoblock appends a sidecar to every managed workload and creates a small TCP endpoint. It periodically fetches volume metrics via the pod IP.
 * How volumes are resized in the running pod?
  When monitoring triggers volume upscale, Discoblocks updates the desired capacity of the PVC. If PVC was attached during pod creation the volumes would be managed by CSI driver. Otherwise, it creates a resize Job, which does the actual resizing on the host.
 * How new volumes are populated to a running pod?
  When monitoring triggers new volume creation, Discoblocks creates a new PersistentVolumeClaim object (and a StorageClass, depending on CSI driver) and a VolumeAttachment. In the final step, it creates a mount Job, which does the actual pod mounting on the host.
 * Why do only the PVCs create at pod startup have the right actual capacity numbers at status?
  The new volumes created during pod runtime are attached only to the node but not to any pod. The CSI drivers usually don't take care of orphan volumes. The volume sizes are managed by Discoblocks until you restart the pod when PVCs and pod became linked.
 * How to delete volumes?
  Discoblocks appends a finalizer to almost every object it touches. Finalizers are removed during the deletion of the DiskConfig. If you have to delete a PVC
    * First please make sure it hasn't attached to any running pod
    * Remove finalizers from PVC, PVCs extending the original one, and the Volume Attachment(s)
    * Delete only the original PVC
    * Kubernetes would do the rest by deleting all other PVCs joined to the original one and other resources
 * Any security-related thoughts?
    * Discoblocks uses Kube native objects to achieve dynamic volume management. Please double-check any RBAC related to Discoblocks
    * Volume metrics data travels in plain text on the "wire", please bring your own encryption layer
    * Volume metrics services are open inside the cluster. Please bring your own network policies to limit access to the endpoints
    * Dicoblocks supports `scratch` and other minimal images at the target container. It mounts custom commands via Busybox (we are planning to replace it) into every managed container.

## Contributing Guidelines
We love your input! We want to make contributing to this project as easy and transparent as possible. You can find the full guidelines [here](https://github.com/ondat/discoblocks/blob/main/CONTRIBUTING.md).

## Community 
Please reach out for any questions or issues via our [Github Discussions](https://github.com/ondat/discoblocks/discussions).

Alternatively you can:
* Raise an issue or PR on this repo
* Follow us on Twitter [@ondat_io](https://twitter.com/ondat_io)

## Roadmap
Project Kanban board: https://github.com/orgs/ondat/projects/2/views/2?layout=board

## License
Discoblocks is under the Apache 2.0 license. See [LICENSE](https://github.com/ondat/discoblocks/blob/main/LICENSE) file for details.
