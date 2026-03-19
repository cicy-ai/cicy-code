.PHONY: dev dev-app dev-api build-app build-api build stop build-all clean release npm-publish ui

VERSION ?= 0.1.0
DIST := dist

# Development
dev-app:
	cd app && npm run dev

dev-api:
	cd api && go run ./mgr/

# Build UI and copy to api/mgr/ui
ui:
	cd app && npm ci && npm run build
	rm -rf api/mgr/ui
	cp -r app/dist api/mgr/ui

# Build
build-app:
	cd app && npm ci && npm run build

build-api: ui
	cd api && CGO_ENABLED=0 go build -ldflags="-s -w" -o cicy-code ./mgr/

build: build-app build-api

# Cross-compile all platforms
build-all: clean ui
	mkdir -p $(DIST)
	cd api && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o ../$(DIST)/cicy-code-linux-amd64 ./mgr/
	cd api && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o ../$(DIST)/cicy-code-linux-arm64 ./mgr/
	cd api && CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o ../$(DIST)/cicy-code-darwin-amd64 ./mgr/
	cd api && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o ../$(DIST)/cicy-code-darwin-arm64 ./mgr/
	ls -lh $(DIST)/

clean:
	rm -rf $(DIST)

# GitHub release
release: build-all
	gh release create v$(VERSION) $(DIST)/* --title "v$(VERSION)" --notes "Release v$(VERSION)"

# npm publish
npm-publish:
	cd npm && npm publish --access public

# Stop
stop:
	@pkill -f "vite" 2>/dev/null || true
	@pkill -f "cicy-code" 2>/dev/null || true
