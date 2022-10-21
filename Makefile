
IMAGE ?= volsync-addon-controller
IMAGE_REGISTRY ?= quay.io/tflower
IMAGE_TAG ?= latest
IMG ?= $(IMAGE_REGISTRY)/$(IMAGE):$(IMAGE_TAG)

OS := $(shell go env GOOS)
ARCH := $(shell go env GOARCH)
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))

# Helper software versions
GOLANGCI_VERSION := v1.50.0

.PHONY: docker-build
docker-build: test ## Build docker image
	docker build -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	docker push ${IMG}

## Location to install dependencies to
LOCALBIN ?= $(PROJECT_DIR)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

.PHONY: test
TEST_ARGS ?= -progress -randomize-all -randomize-suites -slow-spec-threshold=30s -cover -coverprofile=cover.out -output-dir=.
TEST_PACKAGES ?= ./...
test: lint envtest ginkgo ## Run tests.
	-rm -f cover.out
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path)" $(GINKGO) $(TEST_ARGS) $(TEST_PACKAGES)

.PHONY: lint
lint: golangci-lint ## Lint source code
	$(GOLANGCILINT) run --timeout 4m0s ./...

.PHONY: golangci-lint
GOLANGCILINT := $(LOCALBIN)/golangci-lint
GOLANGCI_URL := https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh
golangci-lint: $(GOLANGCILINT) ## Download golangci-lint
$(GOLANGCILINT): $(LOCALBIN)
	curl -sSfL $(GOLANGCI_URL) | sh -s -- -b $(LOCALBIN) $(GOLANGCI_VERSION)

.PHONY: envtest
ENVTEST = $(LOCALBIN)/setup-envtest
envtest: $(ENVTEST) ## Download envtest-setup locally if necessary.
$(ENVTEST): $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest

.PHONY: ginkgo
GINKGO := $(LOCALBIN)/ginkgo
ginkgo: $(GINKGO) ## Download ginkgo
$(GINKGO): $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install github.com/onsi/ginkgo/v2/ginkgo@latest
