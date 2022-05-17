# Local development guide

## Test your change

```console
: ${REPOSITORY?= required}
: ${AWS_ACCOUNT_ID?= required}

# Build and push image
IMG=$REPOSITORY/discoblocks:latest make docker-build docker-push

# Create EKS cluster with EBS
eksctl create cluster --version 1.21 --ssh-access --name $REPOSITORY-test
eksctl utils associate-iam-oidc-provider --cluster $REPOSITORY-test --approve
issuer=$(aws eks describe-cluster --name $REPOSITORY-test --query 'cluster.identity.oidc.issuer' --output text | sed 's|https://||')
now=$(date +'%s')
cat > .aws-ebs-csi-driver-trust-policy.json <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "Federated": "arn:aws:iam::$AWS_ACCOUNT_ID:oidc-provider/$issuer"
      },
      "Action": "sts:AssumeRoleWithWebIdentity",
      "Condition": {
        "StringEquals": {
          "$issuer:aud": "sts.amazonaws.com",
          "$issuer:sub": "system:serviceaccount:kube-system:ebs-csi-controller-sa"
        }
      }
    }
  ]
}
EOF
aws iam create-role --role-name $REPOSITORY-EKS_EBS_CSI_DriverRole-$now --assume-role-policy-document file://'.aws-ebs-csi-driver-trust-policy.json'
aws iam attach-role-policy --policy-arn arn:aws:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy --role-name $REPOSITORY-EKS_EBS_CSI_DriverRole-$now
aws eks create-addon --cluster-name $REPOSITORY-test --addon-name aws-ebs-csi-driver --service-account-role-arn arn:aws:iam::$AWS_ACCOUNT_ID:role/$REPOSITORY-EKS_EBS_CSI_DriverRole-$now
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

# Deploy cert manager
make deploy-cert-manager

# Deploy operator
IMG=$REPOSITORY/discoblocks:latest make manifests generate deploy

# Create sample application
kubectl apply -f config/samples/discoblocks.ondat.io_v1_diskconfig.yaml
kubectl apply -f config/samples/pod.yaml

# Fetch logs
kubectl logs -fn kube-system $(kubectl get po -n kube-system | grep discoblocks-controller-manager | awk '{print $1}')
```