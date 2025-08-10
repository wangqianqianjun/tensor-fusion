# Build the manager binary
FROM golang:1.24 AS builder
ARG TARGETOS
ARG TARGETARCH
ARG GO_LDFLAGS

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer

# Copy the go source
COPY cmd/ cmd/
COPY api/ api/
COPY internal/ internal/
# Copy .git directory to enable VCS info in build
COPY .git/ .git/

COPY scripts/ scripts/
COPY patches/ patches/
RUN go mod vendor && bash ./scripts/patch-scheduler.sh

# Build
# the GOARCH has not a default value to allow the binary be built according to the host where the command
# was called. For example, if we call make docker-build in a local env which has the Apple Silicon M1 SO
# the docker BUILDPLATFORM arg will be linux/arm64 when for Apple x86 it will be linux/amd64. Therefore,
# by leaving it empty we can ensure that the container and binary shipped on it will have the same platform.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -ldflags="$GO_LDFLAGS" -a -o manager ./cmd

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM ubuntu:24.04
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]

# Run locally
# docker build --build-arg TARGETOS=linux --build-arg TARGETARCH=amd64 --build-arg GO_LDFLAGS="-X 'github.com/NexusGPU/tensor-fusion/internal/version.BuildVersion=dev-test'" -t tensorfusion/tensor-fusion-operator:tmp-test . -f dockerfile/operator.Dockerfile
