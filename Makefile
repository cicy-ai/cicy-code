.PHONY: dev dev-app dev-api build-app build-api build stop

# Development
dev-app:
	cd app && npm run dev

dev-api:
	cd api && go run manager.go

# Build
build-app:
	cd app && npm ci && npm run build

build-api:
	cd api && go build -o cicy-code-api ./mgr/

build: build-app build-api

# Stop
stop:
	@pkill -f "vite" 2>/dev/null || true
	@pkill -f "ttyd-manager" 2>/dev/null || true
