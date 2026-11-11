# Shortcuts for common tasks. Without make (e.g. plain Windows), run the command
# shown in each recipe directly.
.DEFAULT_GOAL := help

COMPOSE_DEV  := docker compose -f compose.dev.yaml
COMPOSE_PROD := docker compose -f compose.prod.yaml
BUF          := go run github.com/bufbuild/buf/cmd/buf@v1.75.0

.PHONY: help run fakeai build test test-race cover lint fmt tidy \
        proto proto-lint proto-breaking \
        migrate-up migrate-down migrate-version migrate-create \
        dev dev-db dev-down dev-logs dev-migrate prod-up prod-down prod-logs prod-migrate

help: ## List available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-16s %s\n", $$1, $$2}'

# --- Go (host) ----------------------------------------------------------------
run: ## Run the API on the host (reads .env)
	go run ./cmd/api

fakeai: ## Run the AI service stub on :50051 (set AI_GRPC_ADDR=localhost:50051)
	go run ./cmd/fakeai

build: ## Build binaries into ./bin
	go build -o bin/api ./cmd/api
	go build -o bin/migrate ./cmd/migrate

test: ## Run unit tests
	go test ./...

test-race: ## Run unit tests with the race detector
	go test -race -count=1 ./...

cover: ## Run tests with a coverage summary
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

lint: ## Run golangci-lint (v2)
	golangci-lint run

fmt: ## Format code
	golangci-lint fmt

tidy: ## Tidy go.mod / go.sum
	go mod tidy

# --- gRPC / protobuf --------------------------------------------------------------
proto: ## Lint .proto files and regenerate ./gen
	$(BUF) lint
	$(BUF) generate

proto-lint: ## Lint .proto files
	$(BUF) lint

proto-breaking: ## Check .proto changes for breaking changes against main
	$(BUF) breaking --against '.git#branch=main'

# --- Migrations (host) ----------------------------------------------------------
migrate-up: ## Apply pending migrations
	go run ./cmd/migrate up

migrate-down: ## Roll back the last migration
	go run ./cmd/migrate down 1

migrate-version: ## Show the current schema version
	go run ./cmd/migrate version

migrate-create: ## Create a migration pair: make migrate-create name=create_posts_table
	go run ./cmd/migrate create $(name)

# --- Docker: development --------------------------------------------------------
dev: ## Start db + API with hot reload (REST :3333, gRPC :9090)
	$(COMPOSE_DEV) up

dev-db: ## Start only the dev database (host port 5433)
	$(COMPOSE_DEV) up -d db

dev-down: ## Stop the dev stack
	$(COMPOSE_DEV) down

dev-logs: ## Follow API logs
	$(COMPOSE_DEV) logs -f api

dev-migrate: ## Run a migrate command inside the dev container: make dev-migrate cmd="down 1"
	$(COMPOSE_DEV) exec api go run ./cmd/migrate $(or $(cmd),up)

# --- Docker: production ---------------------------------------------------------
prod-up: ## Build and start the production stack (REST :8000, gRPC :9000; needs .env)
	$(COMPOSE_PROD) up -d

prod-down: ## Stop the production stack
	$(COMPOSE_PROD) down

prod-logs: ## Follow production API logs
	$(COMPOSE_PROD) logs -f api

prod-migrate: ## Run a migrate command in production: make prod-migrate cmd="down 1"
	$(COMPOSE_PROD) run --rm --no-deps --entrypoint /app/migrate api $(or $(cmd),version)
