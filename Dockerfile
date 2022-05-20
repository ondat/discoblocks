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

# Build CSI drivers
FROM tinygo/tinygo:0.23.0 as drivers

COPY drivers/ /go/src

RUN cd /go/src/ebs.csi.aws.com ; go mod tidy && tinygo build -o main.wasm -target wasi --no-debug main.go

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM golang@sha256:5b75b529da0f2196ee8561a90e5b99aceee56e125c6ef09a3da4e32cf3cc6c20
# FROM gcr.io/distroless/static@sha256:2556293984c5738fc75208cce52cf0a4762c709cf38e4bf8def65a61992da0ad

LABEL org.opencontainers.image.title "Discoblocks" 
LABEL org.opencontainers.image.vendor "Discoblocks.io" 
LABEL org.opencontainers.image.licenses "Apache-2.0 License" 
LABEL org.opencontainers.image.source "https://github.com/ondat/discoblocks" 
LABEL org.opencontainers.image.description "Discoblocks is an open-source declarative disk configuration system for Kubernetes helping to automate CRUD (Create, Read, Update, Delete) operations for cloud disk device resources attached to Kubernetes cluster nodes." 
LABEL org.opencontainers.image.documentation "https://github.com/ondat/discoblocks/wiki" 

WORKDIR /
COPY --from=builder /workspace/manager .
COPY --from=builder /go/pkg/mod/github.com/wasmerio/wasmer-go@v1.0.4/wasmer/packaged/lib/linux-amd64/libwasmer.so /lib
# COPY --from=builder /lib/x86_64-linux-gnu/libpthread.so.0 /lib
# COPY --from=builder /lib/x86_64-linux-gnu/libc.so.6 /lib
# COPY --from=builder /lib/x86_64-linux-gnu/libdl.so.2 /lib
# COPY --from=builder /lib/x86_64-linux-gnu/libgcc_s.so.1 /lib
# COPY --from=builder /lib/x86_64-linux-gnu/librt.so.1 /lib
# COPY --from=builder /lib/x86_64-linux-gnu/libm.so.6 /lib
# COPY --from=builder /lib64/ld-linux-x86-64.so.2 /lib
COPY --from=drivers /go/src /drivers

USER 65532:65532

ENTRYPOINT ["/manager"]
