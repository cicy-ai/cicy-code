.PHONY: dev dev-app dev-api build build-all clean release npm-publish stop

VERSION ?= 0.1.0

# Development
dev-app:
	cd app && npm run dev

dev-api:
	cd api && go run ./mgr/

# Build
build:
	./build.sh build

build-all:
	./build.sh all

clean:
	rm -rf dist

# GitHub release
release: build-all
	gh release create v$(VERSION) dist/* --title "v$(VERSION)" --notes "Release v$(VERSION)"

# npm publish
npm-publish:
	cd npm && npm publish --access public

# Stop
stop:
	@pkill -f "vite" 2>/dev/null || true
	@pkill -f "cicy-code" 2>/dev/null || true
