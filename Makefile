.PHONY: dev prod build-ide build-backend clean

# Development: ide hot-reload + backend + mysql
dev:
	docker compose --profile dev up ide-dev backend mysql

# Production: all services
prod:
	docker compose up -d ide backend mysql

# Build
build-ide:
	cd ide && npm ci && npm run build

build-backend:
	cd backend && go build -o ttyd-manager manager.go

build: build-ide build-backend

# Stop
stop:
	docker compose --profile dev down

clean:
	docker compose --profile dev down -v
