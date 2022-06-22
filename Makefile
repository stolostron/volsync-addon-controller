
IMAGE ?= volsync-addon-controller
IMAGE_REGISTRY ?= quay.io/tflower
IMAGE_TAG ?= latest
IMG ?= $(IMAGE_REGISTRY)/$(IMAGE):$(IMAGE_TAG)

OS := $(shell go env GOOS)
ARCH := $(shell go env GOARCH)
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))

# Helper software versions
GOLANGCI_VERSION := v1.46.1

.PHONY: docker-build
docker-build: test ## Build docker image
	docker build -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	docker push ${IMG}

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
GOLANGCILINT := $(PROJECT_DIR)/bin/golangci-lint
GOLANGCI_URL := https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh
golangci-lint: ## Download golangci-lint
ifeq (,$(wildcard $(GOLANGCILINT)))
	mkdir -p $(PROJECT_DIR)/bin
	curl -sSfL $(GOLANGCI_URL) | sh -s -- -b $(PROJECT_DIR)/bin $(GOLANGCI_VERSION)
endif

.PHONY: envtest
ENVTEST = $(shell pwd)/bin/setup-envtest
envtest: ## Download envtest-setup locally if necessary.
	$(call go-get-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest@latest)

.PHONY: ginkgo
GINKGO := $(PROJECT_DIR)/bin/ginkgo
ginkgo: ## Download ginkgo
	$(call go-get-tool,$(GINKGO),github.com/onsi/ginkgo/v2/ginkgo)

# go-get-tool will 'go get' any package $2 and install it to $1.
define go-get-tool
@[ -f $(1) ] || { \
set -e ;\
TMP_DIR=$$(mktemp -d) ;\
cd $$TMP_DIR ;\
go mod init tmp ;\
echo "Downloading $(2)" ;\
GOBIN=$(PROJECT_DIR)/bin go get $(2) ;\
rm -rf $$TMP_DIR ;\
}
endef
