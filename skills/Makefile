BASE_IMAGE ?= cicy-skills-base-user-env:local
SMOKE_IMAGE ?= cicy-skills-google-smoke:local
NO_CACHE ?=
GO ?= go
GOOS ?= linux
GOARCH ?= $(shell $(GO) env GOARCH)
CGO_ENABLED ?= 0

.DEFAULT_GOAL := help

.PHONY: help build-local-binaries install-local-cli test-go test-google-provider test-google-provider-live ensure-base-user-env docker-base-user-env docker-google-smoke run-google-skill-test test-google-skill test-google-skill-no-cache smoke-google

help: ## Show available make targets
	@printf "Targets:\n"
	@awk 'BEGIN {FS = ": ## "}; /^[a-zA-Z0-9_.-]+: ## / {printf "  %-22s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build-local-binaries: ## Build Linux CLI binaries locally into dist/
	mkdir -p dist
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) build -o dist/cicy-skills ./cmd/cicy-skills
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) build -o dist/cicy-skillsd ./cmd/cicy-skillsd
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) build -o dist/cicy-hosttools ./cmd/cicy-hosttools
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) build -o dist/stt ./cmd/stt
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) build -o dist/tts ./cmd/tts

install-local-cli: ## Install the full local command bundle into ~/.local/bin using the repo-owned bin dir
install-local-cli: build-local-binaries
	CICY_SKILLS_ROOT=$(CURDIR) ./dist/cicy-skills install all

test-go: ## Run the Go test suite
	$(GO) test ./...

test-google-provider: ## Run the comprehensive google-node provider test suite
	cd providers/google-node && npm test

test-google-provider-live: ## Run live google-node tests against the real token in ~/cicy-ai/global.json
	cd providers/google-node && npm run test:live

ensure-base-user-env: ## Build the base image only if it does not already exist locally
	@docker image inspect $(BASE_IMAGE) >/dev/null 2>&1 || $(MAKE) docker-base-user-env

docker-base-user-env: ## Build the reusable base image for smoke tests
	docker build $(NO_CACHE) -f docker/base-user-env/Dockerfile -t $(BASE_IMAGE) .

docker-google-smoke: ## Build the Google skill smoke-test image
docker-google-smoke: build-local-binaries
	docker build $(NO_CACHE) --build-arg BASE_IMAGE=$(BASE_IMAGE) -f docker/google-smoke/Dockerfile -t $(SMOKE_IMAGE) .

run-google-skill-test: ## Run the Google skill smoke test in the base container
	BASE_IMAGE=$(BASE_IMAGE) ./docker/google-smoke/run-in-base.sh

test-google-skill: ## Run provider tests, then run the Google skill smoke test in Docker
test-google-skill: test-google-provider build-local-binaries ensure-base-user-env run-google-skill-test

test-google-skill-no-cache: ## Run the Google skill smoke test in Docker without cache
	$(MAKE) test-google-provider
	$(MAKE) build-local-binaries
	$(MAKE) docker-base-user-env NO_CACHE=--no-cache
	$(MAKE) run-google-skill-test

smoke-google: ## Alias for test-google-skill
smoke-google: test-google-skill
