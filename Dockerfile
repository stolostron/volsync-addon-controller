# Build the addon controller binary
#FROM registry.access.redhat.com/ubi8/go-toolset as builder
FROM registry.ci.openshift.org/stolostron/builder:go1.17-linux AS builder
USER root

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY main.go .
COPY controllers/ controllers/

# Build
# We don't vendor modules. Enforce that behavior
ENV GOFLAGS=-mod=readonly
#ARG VERSION="(unknown)" #FIXME: remove
RUN GOOS=linux GOARCH=amd64 GO111MODULE=on go build -a -o controller main.go

# Final container
FROM registry.access.redhat.com/ubi8-minimal

RUN microdnf --refresh update && \
    microdnf clean all

WORKDIR /
COPY --from=builder /workspace/controller .
# uid/gid: nobody/nobody
USER 65534:65534

ENTRYPOINT ["/controller"]
