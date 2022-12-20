# Build CSI drivers
FROM tinygo/tinygo@sha256:65dc1c3e54f88aabe1efe073c3aadb1393593a56355a6ac03df5f18e6c3855dd as drivers

COPY drivers/ /go/src

RUN cd /go/src/csi.storageos.com ; go mod tidy && tinygo build -o main.wasm -target wasi --no-debug main.go
RUN cd /go/src/ebs.csi.aws.com ; go mod tidy && tinygo build -o main.wasm -target wasi --no-debug main.go

# Build the manager binary
FROM golang@sha256:5b75b529da0f2196ee8561a90e5b99aceee56e125c6ef09a3da4e32cf3cc6c20 as builder

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY main.go main.go
COPY api/ api/
COPY controllers/ controllers/
COPY mutators/ mutators/
COPY pkg/ pkg/
COPY schedulers/ schedulers/

# Build
RUN GOOS=linux GOARCH=amd64 go build -a -o manager main.go

# Use UBI as minimal base image to package the manager binary
FROM redhat/ubi8-micro@sha256:4f6f8db9a6dc949d9779a57c43954b251957bd4d019a37edbbde8ed5228fe90a

LABEL org.opencontainers.image.title "Discoblocks"
LABEL org.opencontainers.image.vendor "Discoblocks.io"
LABEL org.opencontainers.image.licenses "Apache-2.0 License"
LABEL org.opencontainers.image.source "https://github.com/ondat/discoblocks"
LABEL org.opencontainers.image.description "Discoblocks is an open-source declarative disk configuration system for Kubernetes helping to automate CRUD (Create, Read, Update, Delete) operations for cloud disk device resources attached to Kubernetes cluster nodes."
LABEL org.opencontainers.image.documentation "https://github.com/ondat/discoblocks/wiki"

WORKDIR /
COPY --from=drivers /go/src /drivers
COPY --from=builder /workspace/manager .
COPY --from=builder /go/pkg/mod/github.com/wasmerio/wasmer-go@v1.0.4/wasmer/packaged/lib/linux-amd64/libwasmer.so /lib64
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

USER 65532:65532

ENTRYPOINT ["/manager"]
