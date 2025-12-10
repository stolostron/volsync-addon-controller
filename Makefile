
IMAGE ?= volsync-addon-controller
IMAGE_REGISTRY ?= quay.io/tflower
IMAGE_TAG ?= latest
IMG ?= $(IMAGE_REGISTRY)/$(IMAGE):$(IMAGE_TAG)

OS := $(shell go env GOOS)
ARCH := $(shell go env GOARCH)
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))

# Helper software versions
GOLANGCI_VERSION := v2.7.2
ENVTEST_K8S_VERSION = 1.30

GO_LD_EXTRAFLAGS ?=

SOURCE_GIT_TAG ?=$(shell git describe --long --tags --abbrev=7 --match 'v[0-9]*' || echo 'v0.0.0-unknown')
SOURCE_GIT_COMMIT ?=$(shell git rev-parse --short "HEAD^{commit}" 2>/dev/null)
DIRTY ?= $(shell git diff --quiet || echo '-dirty')

ifndef GIT_VERSION
	GIT_VERSION = $(SOURCE_GIT_TAG)$(DIRTY)
endif

ifndef GIT_COMMIT
	GIT_COMMIT = $(SOURCE_GIT_COMMIT)$(DIRTY)
endif

define version-ldflags
-X main.versionFromGit=$(GIT_VERSION) \
-X main.commitFromGit=$(GIT_COMMIT)
endef
GO_LD_FLAGS ?="$(call version-ldflags) $(GO_LD_EXTRAFLAGS)"

.PHONY: docker-build
docker-build: test ## Build docker image
	docker build --build-arg "versionFromGit_arg=$(GIT_VERSION)" --build-arg "commitFromGit_arg=$(GIT_COMMIT)" -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	docker push ${IMG}

## Location to install dependencies to
LOCALBIN ?= $(PROJECT_DIR)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

.PHONY: test
TEST_ARGS ?= -show-node-events -randomize-all -randomize-suites -poll-progress-after=30s -cover -coverprofile=cover.out -output-dir=.
TEST_PACKAGES ?= ./...
test: goversion lint envtest ginkgo ## Run tests.
	-rm -f cover.out
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path)" $(GINKGO) $(TEST_ARGS) $(TEST_PACKAGES)

.PHONY: goversion
goversion: ## Just print out go version being used
	go version

.PHONY: lint
lint: goversion golangci-lint ## Lint source code
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

.PHONY: build
build:
	GO111MODULE=on go build -a -o bin/controller -ldflags $(GO_LD_FLAGS) main.go

.PHONY: run
GO_LD_FLAGS_LOCALRUN = "$(call version-ldflags) -X main.embeddedChartsDir=$(PROJECT_DIR)/helmcharts"
run:
	POD_NAMESPACE=open-cluster-management go run -ldflags $(GO_LD_FLAGS_LOCALRUN) . controller --namespace open-cluster-management --kubeconfig $(KUBECONFIG)
