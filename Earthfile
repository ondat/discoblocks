VERSION --use-cache-command 0.7
FROM golang:1.18
WORKDIR /workdir
ARG --global KUBE_VERSION=1.23
ARG --global REGISTRY=ghcr.io
ARG --global IMAGE_NAME=discoblocks
ARG --global IMAGE_TAG=latest

all:
    WAIT
        BUILD +go-lint
        BUILD +go-test
        BUILD +bundle
    END
    WAIT
        BUILD +go-sec
    END
    WAIT
        BUILD +scan-image
    END

go-lint:
    FROM earthly/dind:alpine
    COPY . ./workdir
    WITH DOCKER --pull golangci/golangci-lint:v1.51.0
        RUN docker run -w /workdir -v /workdir:/workdir golangci/golangci-lint:v1.51.0 golangci-lint run --timeout 300s
    END

go-sec:
    FROM earthly/dind:alpine
    COPY . ./workdir
    WITH DOCKER --pull securego/gosec:2.15.0
        RUN docker run -w /workdir -v /workdir:/workdir securego/gosec:2.15.0 -exclude-dir=bin -exclude-dir=drivers -exclude-generated ./...
    END

go-test:
    FROM +deps-go-build
    CACHE $HOME/.cache/go-build
    COPY . ./
    WITH DOCKER --pull tinygo/tinygo:0.23.0
        RUN make _test
    END

e2e-test:
    FROM earthly/dind:alpine
    RUN apk add make bash
    WORKDIR /workdir
    ENV KUSTOMIZE=/usr/local/bin/kustomize
    ARG TIMEOUT=240
    COPY --dir +deps-tooling/* /usr/local
    COPY Makefile ./
    COPY --dir config ./
    COPY --dir tests ./
    WITH DOCKER --load local/discoblocks:e2e=+build-image --load local/discoblocks:job-e2e=+build-job-image --load local/discoblocks:proxy-e2e=+build-proxy-image
        RUN kubectl-kuttl test --timeout ${TIMEOUT} --config tests/e2e/kuttl/kuttl-config-${KUBE_VERSION}.yaml || touch /failure
    END

    IF [ -d kind-logs-* ]
        SAVE ARTIFACT --if-exists kind-logs-* AS LOCAL ./
    END

    IF [ -f /failure ]
        RUN echo "e2e test run failed" && exit 1
    END

build-drivers:
    FROM tinygo/tinygo:0.23.0
    COPY --dir drivers /go/src
    WAIT
        RUN cd /go/src/csi.storageos.com ; go mod tidy && tinygo build -o main.wasm -target wasi --no-debug main.go
    END
    WAIT
        RUN cd /go/src/ebs.csi.aws.com ; go mod tidy && tinygo build -o main.wasm -target wasi --no-debug main.go
    END

    SAVE ARTIFACT /go/src/csi.storageos.com /drivers/csi.storageos.com
    SAVE ARTIFACT /go/src/ebs.csi.aws.com /drivers/ebs.csi.aws.com

build-operator:
    FROM +deps-go
    CACHE $HOME/.cache/go-build
    COPY main.go ./
    COPY --dir api ./
    COPY --dir controllers ./
    COPY --dir mutators ./
    COPY --dir pkg ./
    COPY --dir schedulers ./
    RUN GOOS=linux GOARCH=amd64 go build -a -o manager main.go

    SAVE ARTIFACT manager
    SAVE ARTIFACT /go/pkg/mod /go/pkg/mod
    SAVE ARTIFACT /etc/ssl/certs /etc/ssl/certs

build-all-images:
    WAIT
        BUILD +build-image
    END
    WAIT
        BUILD +build-job-image
    END
    WAIT
        BUILD +build-proxy-image
    END

build-job-image:
    FROM DOCKERFILE -f +deps-job-image/Dockerfile .

    SAVE IMAGE --push  ${REGISTRY}/${IMAGE_NAME}:job-${IMAGE_TAG}

build-proxy-image:
    FROM DOCKERFILE -f +deps-proxy-image/Dockerfile .

    SAVE IMAGE --push  ${REGISTRY}/${IMAGE_NAME}:proxy-${IMAGE_TAG}

build-image:
    FROM redhat/ubi8-micro@sha256:4f6f8db9a6dc949d9779a57c43954b251957bd4d019a37edbbde8ed5228fe90a
    WORKDIR /
    LABEL org.opencontainers.image.title="Discoblocks"
    LABEL org.opencontainers.image.vendor="Discoblocks.io"
    LABEL org.opencontainers.image.licenses="Apache-2.0 License"
    LABEL org.opencontainers.image.source="https://github.com/ondat/discoblocks"
    LABEL org.opencontainers.image.description="Discoblocks is an open-source declarative disk configuration system for Kubernetes helping to automate CRUD (Create, Read, Update, Delete) operations for cloud disk device resources attached to Kubernetes cluster nodes."
    LABEL org.opencontainers.image.documentation="https://github.com/ondat/discoblocks/wiki"
    COPY +build-drivers/drivers /drivers
    COPY +build-operator/manager /manager
    COPY +build-operator/go/pkg/mod/github.com/wasmerio/wasmer-go@v1.0.4/wasmer/packaged/lib/linux-amd64/libwasmer.so /lib64
    COPY +build-operator/etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
    USER 65532:65532
    ENTRYPOINT ["/manager"]

    SAVE IMAGE --push ${REGISTRY}/${IMAGE_NAME}:${IMAGE_TAG}

scan-image:
    FROM earthly/dind:alpine
    WITH DOCKER --load local/discoblocks:trivy=+build-image --pull aquasec/trivy:0.38.1
        RUN docker run -w /workdir -v ${PWD}:/workdir -v /var/run/docker.sock:/var/run/docker.sock aquasec/trivy:0.38.1 image -f sarif -o trivy-results.sarif -s 'CRITICAL,HIGH' local/discoblocks:trivy
    END

    SAVE ARTIFACT --if-exists trivy-results.sarif AS LOCAL trivy-results.sarif

bundle:
    FROM +deps-go-build
    ENV KUSTOMIZE=/usr/local/bin/kustomize
    COPY --dir +deps-tooling/* /usr/local
    COPY Makefile ./
    COPY --dir config ./
    RUN sed -i "s|$(grep jobContainerImage config/manager/controller_manager_config.yaml | awk '{print $2}')|${REGISTRY}/${IMAGE_NAME}:job-${IMAGE_TAG}|" config/manager/controller_manager_config.yaml
    RUN sed -i "s|$(grep proxyContainerImage config/manager/controller_manager_config.yaml | awk '{print $2}')|${REGISTRY}/${IMAGE_NAME}:proxy-${IMAGE_TAG}|" config/manager/controller_manager_config.yaml
    RUN make _bundle

    SAVE ARTIFACT --if-exists discoblocks-bundle.yaml AS LOCAL discoblocks-bundle.yaml
    SAVE ARTIFACT --if-exists discoblocks-kustomize.tar.gz AS LOCAL discoblocks-kustomize.tar.gz

deps-go:
    COPY go.mod go.sum ./
    RUN go mod download

deps-go-build:
    FROM +deps-go
    COPY Makefile ./
    RUN make controller-gen envtest

deps-tooling:
    SAVE ARTIFACT /usr/local/go /
    SAVE ARTIFACT /usr/local/go/bin /

    WAIT
        ARG LATEST_VERSION=$(curl -s https://api.github.com/repos/storageos/kubectl-storageos/releases/latest | grep tag_name | head -1 | awk -F'\"' '{ print $4 }' | tr -d v)
        RUN curl -sSL https://github.com/storageos/kubectl-storageos/releases/download/v${LATEST_VERSION}/kubectl-storageos_${LATEST_VERSION}_linux_amd64.tar.gz | tar -xz
        RUN chmod +x kubectl-storageos
        SAVE ARTIFACT kubectl-storageos /bin/kubectl-storageos
    END

    WAIT
        RUN curl -sLo kubectl-kuttl https://github.com/kudobuilder/kuttl/releases/download/v0.15.0/kubectl-kuttl_0.15.0_linux_x86_64
        RUN chmod +x kubectl-kuttl
        SAVE ARTIFACT kubectl-kuttl /bin/kubectl-kuttl
    END

    WAIT
        RUN curl -sLO https://dl.k8s.io/release/v${KUBE_VERSION}.0/bin/linux/amd64/kubectl
        RUN chmod +x kubectl
        SAVE ARTIFACT kubectl /bin/kubectl
    END

    WAIT
        COPY Makefile ./
        RUN make kustomize
        SAVE ARTIFACT bin/kustomize /bin/kustomize
    END

deps-job-image:
    COPY config/manager/controller_manager_config.yaml ./
    RUN echo "FROM $(grep "jobContainerImage" controller_manager_config.yaml | awk '{print $2}')" > Dockerfile

    SAVE ARTIFACT Dockerfile

deps-proxy-image:
    COPY config/manager/controller_manager_config.yaml ./
    RUN echo "FROM $(grep "proxyContainerImage" controller_manager_config.yaml | awk '{print $2}')" > Dockerfile

    SAVE ARTIFACT Dockerfile