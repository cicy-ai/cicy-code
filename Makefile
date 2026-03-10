.PHONY: dev dev-ide dev-backend build-ide build-backend build stop

# Development
dev-ide:
	cd ide && npm run dev

dev-backend:
	cd backend && go run manager.go

# Build
build-ide:
	cd ide && npm ci && npm run build

build-backend:
	cd backend && go build -o ttyd-manager manager.go

build: build-ide build-backend

# Stop
stop:
	@pkill -f "vite" 2>/dev/null || true
	@pkill -f "ttyd-manager" 2>/dev/null || true
