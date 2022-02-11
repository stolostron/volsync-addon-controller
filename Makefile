
IMAGE ?= volsync-addon-controller
IMAGE_REGISTRY ?= quay.io/tflower
IMAGE_TAG ?= latest
IMG ?= $(IMAGE_REGISTRY)/$(IMAGE):$(IMAGE_TAG)

OS := $(shell go env GOOS)
ARCH := $(shell go env GOARCH)
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))

# Helper software versions
GOLANGCI_VERSION := v1.43.0
HELM_VERSION := v3.7.1

.PHONY: docker-build
#docker-build: test ## Build docker image with the manager.
docker-build:
	docker build -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	docker push ${IMG}

.PHONY: lint
lint: golangci-lint ## Lint source code
	$(GOLANGCILINT) run ./...

.PHONY: golangci-lint
GOLANGCILINT := $(PROJECT_DIR)/bin/golangci-lint
GOLANGCI_URL := https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh
golangci-lint: ## Download golangci-lint
ifeq (,$(wildcard $(GOLANGCILINT)))
	mkdir -p $(PROJECT_DIR)/bin
	curl -sSfL $(GOLANGCI_URL) | sh -s -- -b $(PROJECT_DIR)/bin $(GOLANGCI_VERSION)
endif

.PHONY: helm-lint
helm-lint: helm ## Lint Helm chart
	cd helm-charts && $(HELM) lint volsync-addon-controller

.PHONY: helm
HELM := $(PROJECT_DIR)/bin/helm
HELM_URL := https://get.helm.sh/helm-$(HELM_VERSION)-$(OS)-$(ARCH).tar.gz
helm: ## Download helm
ifeq (,$(wildcard $(HELM)))
	mkdir -p $(PROJECT_DIR)/bin
	curl -sSL "$(HELM_URL)" | tar xzf - -C $(PROJECT_DIR)/bin --strip-components=1 --wildcards '*/helm'
endif