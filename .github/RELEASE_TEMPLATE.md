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
volumeBindingMode: WaitForFirstConsumer
EOF

kubectl apply -f https://github.com/ondat/discoblocks/releases/download/#VERSION#/discoblocks-bundle.yaml

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

### Build your own version
``` console
git clone -b #VERSION# https://github.com/ondat/discoblocks.git
cd discoblocks
export IMG=myregistry/discconfig:current
make docker-build docker-push deploy bundle
kubectl apply -f discoblocks-bundle.yaml
```