# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is the volsync-addon-controller, an addon controller for Red Hat Advanced Cluster Management (ACM). It deploys the VolSync operator on managed clusters when ManagedClusterAddOn CRs are created.

## Development Commands

### Building and Testing

```bash
# Run all tests with coverage
make test

# Run linting
make lint

# Build the controller binary
make build

# Build Docker image
make docker-build

# Run the controller locally pointing to a remote cluster
make run
```

### Key Test Commands

- `make test` - Runs the full test suite using Ginkgo with coverage reporting
- Test files follow the `*_test.go` pattern and use Ginkgo/Gomega testing framework
- Tests are located in the `controllers/` directory alongside the main code

### Go Environment

- Go version: 1.24.4 (see go.mod)
- Uses golangci-lint for code quality checks
- Configuration in `.golangci.yml` with additional linters enabled

## Architecture

### Core Components

1. **Main Controller** (`controllers/addoncontroller.go`)
   - Uses the Open Cluster Management addon framework
   - Implements the `volsyncAgent` that manages VolSync deployments
   - Deploys VolSync via Helm charts (ACM 2.13+) or OLM subscriptions (legacy)

2. **Label-based Installation** (`controllers/addon_installbylabel_controller.go`)
   - Monitors ManagedCluster resources for the label `addons.open-cluster-management.io/volsync="true"`
   - Automatically creates ManagedClusterAddOn resources when the label is present

3. **Manifest Helpers**
   - `manifesthelper_helmdeploy.go` - Handles Helm-based deployments (current approach)
   - `manifesthelper_operatordeploy.go` - Handles OLM subscription deployments (legacy)

4. **Helm Utilities** (`controllers/helmutils/`)
   - Manages embedded Helm charts for VolSync deployment
   - Handles chart templating and customization

### Deployment Modes

- **Modern (ACM 2.13+)**: Deploys VolSync as a Helm chart on managed clusters
- **Legacy (ACM 2.12-)**: Deploys VolSync as an OpenShift operator via OLM subscription

### Key Directories

- `controllers/` - Main controller logic and tests
- `controllers/helmutils/` - Helm chart management utilities
- `controllers/manifests/` - YAML manifests for deployments
- `helmcharts/` - Embedded Helm charts for VolSync
- `hack/` - Development and testing utilities

### Integration Points

- Open Cluster Management addon framework for managed cluster integration
- Kubernetes controller-runtime for controller implementation
- Helm v3 for chart management and deployment
- ACM's MultiClusterHub (MCH) for default image configurations

## Local Development

When running locally with `make run`, the controller:
- Runs in the `open-cluster-management` namespace
- Uses embedded charts from the local `helmcharts/` directory
- Requires KUBECONFIG to be set for cluster access
- Loads default VolSync images from ACM's MCH image-manifest configmap