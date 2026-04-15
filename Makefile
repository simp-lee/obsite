.DEFAULT_GOAL := check

.PHONY: fmt fmt-check lint test check install-pagefind

GO ?= go
GO_ENV_GOBIN := $(strip $(shell $(GO) env GOBIN 2>/dev/null))
GO_ENV_GOPATH := $(strip $(shell $(GO) env GOPATH 2>/dev/null))
GO_BIN_DIR := $(if $(GO_ENV_GOBIN),$(GO_ENV_GOBIN),$(if $(GO_ENV_GOPATH),$(GO_ENV_GOPATH)/bin))

ifneq ($(GO_BIN_DIR),)
export PATH := $(GO_BIN_DIR):$(PATH)
endif

PKGS ?= ./...
GOLANGCI_LINT ?= golangci-lint
GOFUMPT ?= gofumpt
GOIMPORTS ?= goimports
PROJECT_DIR ?= .

fmt:
	@command -v $(GOIMPORTS) >/dev/null 2>&1 || { echo "$(GOIMPORTS) not found; install with: go install golang.org/x/tools/cmd/goimports@latest"; exit 1; }
	@command -v $(GOFUMPT) >/dev/null 2>&1 || { echo "$(GOFUMPT) not found; install with: go install mvdan.cc/gofumpt@latest"; exit 1; }
	@files="$$(find . -type f -name '*.go' -not -path './vendor/*')"; \
	if [ -n "$$files" ]; then $(GOIMPORTS) -w $$files && $(GOFUMPT) -w $$files; fi

fmt-check:
	@command -v $(GOIMPORTS) >/dev/null 2>&1 || { echo "$(GOIMPORTS) not found; install with: go install golang.org/x/tools/cmd/goimports@latest"; exit 1; }
	@command -v $(GOFUMPT) >/dev/null 2>&1 || { echo "$(GOFUMPT) not found; install with: go install mvdan.cc/gofumpt@latest"; exit 1; }
	@files="$$(find . -type f -name '*.go' -not -path './vendor/*')"; \
	if [ -z "$$files" ]; then exit 0; fi; \
	imports_out="$$( $(GOIMPORTS) -l $$files )" || exit 1; \
	format_out="$$( $(GOFUMPT) -l $$files )" || exit 1; \
	out="$$(printf '%s\n%s\n' "$$imports_out" "$$format_out" | awk 'NF && !seen[$$0]++')"; \
	if [ -n "$$out" ]; then printf '%s\n' "$$out"; exit 1; fi

lint:
	@command -v $(GOLANGCI_LINT) >/dev/null 2>&1 || { echo "$(GOLANGCI_LINT) not found"; exit 1; }
	$(GOLANGCI_LINT) run $(PKGS)

test:
	$(GO) test $(PKGS)

install-pagefind:
	sh scripts/install-pagefind.sh "$(PROJECT_DIR)"

check: fmt-check lint test