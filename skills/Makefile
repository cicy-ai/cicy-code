GO ?= go
GOOS ?= linux
GOARCH ?= $(shell $(GO) env GOARCH)
CGO_ENABLED ?= 0

.DEFAULT_GOAL := help

.PHONY: help build-local-binaries test-go

help: ## Show available make targets
	@printf "Targets:\n"
	@awk 'BEGIN {FS = ": ## "}; /^[a-zA-Z0-9_.-]+: ## / {printf "  %-22s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build-local-binaries: ## Build Linux CLI binaries locally into dist/
	mkdir -p dist
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) build -o dist/stt ./cmd/stt
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) build -o dist/tts ./cmd/tts

test-go: ## Run the Go test suite
	$(GO) test ./...
