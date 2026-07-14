# loafer Makefile
#
# Tool versions are pinned here so builds are reproducible. Tools are
# installed into ./bin with `go install` and never touch the host.

SHELL := /usr/bin/env bash -o pipefail

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.date=$(DATE)

IMG ?= ghcr.io/spideyfusion/loafer:$(VERSION)

# Pinned tool versions
GOLANGCI_LINT_VERSION ?= v2.12.2
SETUP_ENVTEST_VERSION ?= release-0.21
KIND_VERSION          ?= v0.29.0
ENVTEST_K8S_VERSION   ?= 1.33.0

LOCALBIN := $(shell pwd)/bin
GOLANGCI_LINT := $(LOCALBIN)/golangci-lint
SETUP_ENVTEST := $(LOCALBIN)/setup-envtest
KIND          := $(LOCALBIN)/kind

# Minimum total test coverage (percent), enforced by `make coverage`.
COVER_MIN ?= 85

.DEFAULT_GOAL := help

##@ General

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ Development

.PHONY: build
build: ## Build the binary into ./bin
	CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o $(LOCALBIN)/loafer ./cmd/loafer

.PHONY: run
run: ## Run against the current kubeconfig context
	go run ./cmd/loafer --config=examples/config.yaml

.PHONY: fmt
fmt: ## gofmt all packages
	go fmt ./...

.PHONY: vet
vet: ## go vet all packages
	go vet ./...

.PHONY: lint
lint: $(GOLANGCI_LINT) ## Run golangci-lint
	$(GOLANGCI_LINT) run

.PHONY: tidy
tidy: ## go mod tidy
	go mod tidy

##@ Testing

.PHONY: test
test: envtest ## Run unit + envtest integration tests with coverage
	KUBEBUILDER_ASSETS="$$($(SETUP_ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" \
		go test ./... -coverprofile=cover.out -covermode=atomic

.PHONY: coverage
coverage: test ## Enforce the coverage gate (excludes cmd/ wiring)
	@grep -v -e '/cmd/' cover.out > cover.filtered.out
	@total=$$(go tool cover -func=cover.filtered.out | awk '/^total:/ {sub(/%/, "", $$3); print $$3}'); \
	echo "total coverage: $$total% (minimum $(COVER_MIN)%)"; \
	awk -v t="$$total" -v m="$(COVER_MIN)" 'BEGIN { exit (t+0 < m+0) ? 1 : 0 }' || \
		{ echo "coverage $$total% is below the $(COVER_MIN)% gate"; exit 1; }

.PHONY: e2e
e2e: $(KIND) build ## Run the kind end-to-end smoke test
	KIND=$(KIND) IMG=$(IMG) hack/e2e.sh

##@ Build

.PHONY: docker-build
docker-build: ## Build the container image
	docker build -t $(IMG) --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) .

##@ Tools

$(LOCALBIN):
	mkdir -p $(LOCALBIN)

$(GOLANGCI_LINT): $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

.PHONY: envtest
envtest: $(SETUP_ENVTEST)
$(SETUP_ENVTEST): $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@$(SETUP_ENVTEST_VERSION)

$(KIND): $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/kind@$(KIND_VERSION)
