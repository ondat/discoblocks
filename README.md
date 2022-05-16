
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
  <a href="https://bestpractices.coreinfrastructure.org/en/projects/6047">
    <img alt="OpenSSF Best Practices" src="https://bestpractices.coreinfrastructure.org/badge_static/85"/>
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

Discoblocks can be leveraged by cloud native data management platform (like [Ondat.io](https://ondat.io)) to management the backend disks in the cloud.  

When using such data management platform to overcome the block disk device limitation from hyperscalers, a new set of manual operational tasks needs to be considered like:
- provisioning block devices on the Kubernetes worker nodes 
- partioning, formating, mounting the block devices within specific path (like /var/lib/vendor) 
- capacity management and monitoring
- resizing and optimizing layouts related to capacity management
- decommissioning the devices in secure way

At the current stage, Discoblocks is leveraging the available hyperscaler CSI (Container Storage Interface) within the Kubernetes cluster to:
- introduce a CRD (Custom Resource Definition) per workload with
  - capacity
  - mount path within the Pod 
  - nodeSelector
  - podSelector
  - upscale policy 
- provision the relevant disk device using the CSI (like EBS on AWS) when the workload deployment will happen
- monitore the volume(s)
- reszie automatically the volume based on the upscale policy

**Note:** that an application could be using Discoblocks to get persistent storage but this option would not be safe for production as there will not be any data platform management to address high availability, replication, fencing, encryption, ...

## About the name 
Some call storage snorage because they believe it is boring... but what we could have fun and dance with the block devices!

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
