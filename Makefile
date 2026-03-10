.PHONY: dev dev-ide dev-api build-ide build-api build stop

# Development
dev-ide:
	cd ide && npm run dev

dev-api:
	cd api && go run manager.go

# Build
build-ide:
	cd ide && npm ci && npm run build

build-api:
	cd api && go build -o cicy-code-api ./mgr/

build: build-ide build-api

# Stop
stop:
	@pkill -f "vite" 2>/dev/null || true
	@pkill -f "ttyd-manager" 2>/dev/null || true
