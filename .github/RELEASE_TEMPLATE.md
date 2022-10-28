# How to try it

## Prerequisite
 * Kubernetes cluster
 * Kubeconfig to deploy
 * Configured AWS EBS CSI driver
 * Installed Cert Manager (`make deploy-cert-manager` should help)

## How to try it
```console
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
volumeBindingMode: Immediate
EOF

kubectl apply -f https://github.com/ondat/discoblocks/releases/download/#VERSION#/discoblocks_#VERSION#.yaml
kubectl apply -f https://github.com/ondat/discoblocks/releases/download/#VERSION#/discoblocks.ondat.io_v1_diskconfig-ebs.csi.aws.com.yaml
kubectl apply -f https://github.com/ondat/discoblocks/releases/download/#VERSION#/core_v1_pod.yaml
```

### Build your own version
``` console
git clone -b #VERSION# https://github.com/ondat/discoblocks.git
cd discoblocks
export IMG=myregistry/discconfig:current
make docker-build docker-push deploy
```