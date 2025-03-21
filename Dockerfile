# Build the addon controller binary
FROM registry.ci.openshift.org/stolostron/builder:go1.23-linux AS builder
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
ENV CGO_ENABLED=1
ARG versionFromGit_arg="(unknown)"
ARG commitFromGit_arg="(unknown)"
RUN GOOS=linux GOARCH=amd64 GO111MODULE=on go build -a -o controller -ldflags "-X main.versionFromGit=${versionFromGit_arg} -X main.commitFromGit=${commitFromGit_arg}" main.go

# Final container
FROM registry.access.redhat.com/ubi9-minimal:latest

RUN microdnf -y --refresh update && \
    microdnf clean all

WORKDIR /
COPY --from=builder /workspace/controller .
# VolSync helm charts
COPY helmcharts/ helmcharts/
# uid/gid: nobody/nobody
USER 65534:65534

ENV EMBEDDED_CHARTS_DIR=/helmcharts

ENTRYPOINT ["/controller"]
