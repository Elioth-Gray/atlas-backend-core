# ATLAS Backend — Project Conventions

These project-specific rules come first and take precedence over the generic rule sets further down.

## Stack

- REST API with Gin for the frontend (see "Go Gin Agent Rules" below).
- gRPC for service-to-service communication with other backends (see "gRPC (Service-to-Service)" below and "Go gRPC Agent Rules"). Both run in the same process and share the same services.
- PostgreSQL 17, accessed exclusively through the GORM ORM (pgx driver underneath).
- Schema changes through versioned SQL migrations (golang-migrate), embedded into the binaries.
- Docker for both development (hot reload) and production (distroless image).
- Configuration only through environment variables (`internal/config`); `.env` is a local convenience and is never committed.

## Project Layout

```
cmd/api             entrypoint: config -> logger -> database -> wiring -> REST + gRPC servers
cmd/migrate         migration CLI (up, down, version, force, create)
proto/              gRPC contracts (.proto), the source of truth for service-to-service APIs
gen/                code generated from proto/ by buf (committed, never edited by hand)
internal/config     env parsing + validation
internal/router     InitRouter(deps): global middleware, route groups
internal/middleware request ID, access log, recovery, security headers, CORS, body limit
internal/handler    thin REST handlers, request/response DTOs, handleError
internal/grpcserver gRPC server, interceptors, thin RPC handlers, toStatus
internal/grpcclient connections to other backends' gRPC servers
internal/service    business logic shared by REST and gRPC, defines the repository interfaces it needs
internal/repository GORM persistence, the only place that touches *gorm.DB
internal/domain     entities (GORM models via struct tags only)
internal/apperror   transport-agnostic domain errors
internal/httpx      REST response envelope, APIError
internal/validation validation messages shared by REST and gRPC
internal/database   GORM/pgx connection, Transactor, migrations runner
migrations/         NNNNNN_name.up.sql / .down.sql
```

Request flow (REST): router → middleware → handler (bind DTO) → service (business rules, `apperror`) → repository (GORM) → PostgreSQL.
Request flow (gRPC): interceptors → RPC handler (validate, map proto ↔ service input) → the same service → repository → PostgreSQL.

## API Conventions

- Version every route under `/api/v1`. Use plural nouns (`/users`, `/users/:id`), `PATCH` for partial updates, and `204 No Content` for deletes.
- Success bodies: `{"data": {...}}` for one resource, `{"data": [...], "meta": {"page", "page_size", "total", "total_pages"}}` for lists.
- Error bodies: `{"error": {"code": "NOT_FOUND", "message": "...", "details": [{"field": "...", "message": "..."}]}}`. Codes are defined in `internal/httpx`.
- JSON field names are `snake_case`. Timestamps are RFC 3339 in UTC. IDs are UUIDs.
- Every list endpoint is paginated (`page`, `page_size`; max page size 100).
- `/healthz` is liveness (no dependencies); `/readyz` checks the database. Neither is versioned.

## gRPC (Service-to-Service)

- The frontend only talks REST. gRPC is for other backends: they call ATLAS through `internal/grpcserver` (port `9090`), and ATLAS calls them through `internal/grpcclient`.
- Contracts live in `proto/<domain>/v1/*.proto` with `package atlas.<domain>.v1;`. Share these files with the other backend teams; they generate their own clients from them.
- After changing a `.proto`, run `make proto` (or `go run github.com/bufbuild/buf/cmd/buf@v1.75.0 lint` then `... generate`) and commit `proto/` and `gen/` together. Never edit `gen/` by hand. Before merging, run `make proto-breaking`; breaking changes need a new version package (`v2`) instead.
- RPC handlers stay thin and call the same `internal/service` services as the REST handlers. Business logic is never duplicated per transport.
- Validate every request field at the start of each RPC with `badRequest` (`check`, `uuid`, `add`); it returns `INVALID_ARGUMENT` with `google.rpc.BadRequest` field violations. Partial updates use `google.protobuf.FieldMask` and only read the masked fields.
- Convert service errors with `toStatus(ctx, err)`: `apperror` kinds map to `InvalidArgument`, `NotFound`, `AlreadyExists`, `Unauthenticated`, `PermissionDenied`, `Unavailable`; anything else is logged and returned as `Internal` without details.
- Authentication is a shared bearer token: callers send `authorization: Bearer <GRPC_AUTH_TOKEN>`. It is required in production (at least 32 characters). Health checks and, outside production, reflection are public.
- Request IDs travel in `x-request-id` metadata in both directions, so logs can be correlated across services. Always pass the incoming `ctx` to outgoing gRPC calls.
- Outgoing calls: create one `grpcclient.New(...)` connection per downstream service at startup (target and token come from new env vars such as `PAYMENTS_GRPC_ADDR` / `PAYMENTS_GRPC_TOKEN` added to `internal/config`), wrap it in the generated client, inject it into the service through a small interface, and close it on shutdown. Every call needs a deadline (either the caller's ctx or `Config.Timeout`).
- Use TLS (`GRPC_TLS_CERT_FILE` / `GRPC_TLS_KEY_FILE` on the server, `Config.TLS` on the client) whenever gRPC traffic leaves a private network.

## Database & ORM (GORM)

- Use GORM for all data access. Do not write raw SQL in application code (`db.Raw`, `db.Exec` with SQL strings, string-built queries). SQL lives only in `migrations/`. If something truly cannot be expressed with GORM, isolate it in the repository with a comment explaining why and flag it in review.
- `*gorm.DB` is only used in `internal/repository` and `internal/database`. Services and handlers never import GORM.
- Start every query with `database.Conn(ctx, r.db)`: it binds the request context (cancellation, deadlines, request-ID logging) and joins the active transaction, if any.
- Pass values only through placeholders: `Where("email = ?", email)`. Never concatenate or `fmt.Sprintf` user input into conditions, and whitelist any user-supplied column names used in `Order`, `Select`, or `Group`.
- Do not use struct conditions for lookups (`Where(&User{ID: id})`): GORM silently drops zero-value fields, so a zero ID matches every row. Use `Where("id = ?", id)`.
- For partial updates use `Updates(map[string]any{...})` (or `Select(...)` + struct) so zero values (`""`, `0`, `false`) are written; `Updates(struct)` skips them. Use `Clauses(clause.Returning{})` to get the updated row back in one round trip.
- Check `RowsAffected` after updates and deletes to detect missing rows.
- Translate errors in the repository: `gorm.ErrRecordNotFound` → `apperror.NotFound`, `gorm.ErrDuplicatedKey` → `apperror.Conflict`, `gorm.ErrForeignKeyViolated` → `apperror.InvalidInput` or `apperror.Conflict`. Wrap everything else with `fmt.Errorf("operation: %w", err)`. GORM/SQL error text never reaches API clients.
- Run multi-step writes in a transaction with `database.Transactor.WithinTx(ctx, func(ctx context.Context) error { ... })`, injected into the service through a small interface. Repositories pick up the transaction automatically through `database.Conn`.
- Avoid N+1 queries: load associations with `Preload` / `Joins`, and paginate every list query with `Limit` / `Offset`.
- Entities live in `internal/domain`, carry `gorm` struct tags, and define `TableName()`. They are never used as API DTOs.
- Never use `AutoMigrate`. Every schema change is a new migration pair created with `go run ./cmd/migrate create <name>`. Keep `up` and `down` symmetrical, never edit a migration that has already been merged or applied (add a new one instead), and keep `gorm` tags consistent with the schema.
- Slow queries (`DB_SLOW_QUERY_THRESHOLD`) and query errors are logged automatically. Set `DB_LOG_QUERIES=true` locally to log every statement; bound parameter values are never logged.

## Docker

The setup follows the same conventions as the team's frontend Docker guide ("Panduan Docker Frontend + Nginx"), so both repositories are run the same way.

- One multi-stage `Dockerfile` with four stages: `deps` (Go modules, shared), `dev` (air hot reload), `build` (static binaries), `prod` (`distroless/static:nonroot` with only the `api` and `migrate` binaries: no toolchain, no source, no shell).
- Two compose files, each building one target and each with its own `name:` (`atlas-backend-dev`, `atlas-backend-prod`), so dev and prod can run side by side with separate containers, networks, and volumes. Both use `pull_policy: build`, so every `up` rebuilds (unchanged steps come from cache).
- One `.env` (copied from `.env.example`) is read by both modes and by `go run`. The compose files override what must differ inside containers (`APP_ENV`, listen ports, `DATABASE_URL` built from `POSTGRES_*`). `.env` is never committed and never copied into an image (`.dockerignore`).
- Unlike the frontend, configuration is read at runtime, not baked in at build time: one image serves every environment.
- Host ports are chosen to not clash with the frontend (dev `5173`, prod `8080`) or each other:

  | Mode | Command | REST | gRPC | PostgreSQL |
  | --- | --- | --- | --- | --- |
  | Development | `docker compose -f compose.dev.yaml up` | `localhost:3333` | `localhost:9090` | `localhost:5433` |
  | Production | `docker compose -f compose.prod.yaml up -d` | `localhost:8000` | `localhost:9000` | not published |

  Inside the containers the API always listens on `8080` (REST) and `9090` (gRPC).
- Development mounts only the Go source (`cmd/`, `internal/`, `migrations/`, `gen/`); `go.mod`, modules, and `.air.toml` come from the image. Changes outside the mounted folders need `down` then `up` (the rebuild is automatic). air polls for changes because file events from Windows/macOS hosts do not reach the container.
- Both modes apply pending migrations on startup (`DB_AUTO_MIGRATE=true`, guarded by a PostgreSQL advisory lock). Manual migration commands: `make dev-migrate cmd="down 1"` / `make prod-migrate cmd=version`.
- `compose.prod.yaml` refuses to start without `POSTGRES_*` and a `GRPC_AUTH_TOKEN` in `.env`, uses `restart: unless-stopped`, and does not publish the database port. For a managed PostgreSQL, remove the `db` service and point `DATABASE_URL` at it.
- Pin image and tool versions (`golang:1.26-alpine`, `postgres:17-alpine`, `air@v1.67.4`) and bump them deliberately in a dedicated change.
- Production containers run as non-root and stay stateless: persistent data belongs in PostgreSQL volumes or external storage.
- When adding a runtime dependency (Redis, object storage, a webhook worker, ...), add it to both compose files with a healthcheck, document its variables in `.env.example`, and wire it through `internal/config`.
- This repository has no CI/CD and none should be added (no `.github/workflows`, no deploy pipelines). Checks run locally: before merging a change that touches the `Dockerfile`, compose files, or dependencies, build the production image (`docker build --target prod .`) and run `go test ./...` and `golangci-lint run`.

## Adding a Feature

1. Migration: `go run ./cmd/migrate create create_<things>_table`, write `up` and `down`.
2. Entity in `internal/domain` with `gorm` tags and `TableName()`.
3. Repository in `internal/repository` (GORM only, `database.Conn(ctx, r.db)`, errors translated to `apperror`).
4. Service in `internal/service`: declares the repository interface it needs, enforces business rules, returns `apperror` values.
5. Handler + DTOs in `internal/handler`: bind, call the service, map to response DTOs, route all errors through `handleError`.
6. Register routes in `internal/router` under `/api/v1`.
7. If other backends need it: add RPCs to `proto/`, run `make proto`, implement them in `internal/grpcserver` on top of the same service, and register new gRPC services in `grpcserver.New`.
8. Tests: handler (httptest + fake service), RPCs (bufconn via `testServer`), and service (fake repository), all table-driven.

# Go Gin Agent Rules

## Project Context

You are building a REST API with the Gin web framework. Gin provides fast routing, middleware chaining, and request binding while staying close to idiomatic Go patterns.

## Code Style & Structure

- Organize routes in a dedicated `router/` package; mount route groups inside an `InitRouter(deps) *gin.Engine` function.
- Use `gin.New()` instead of `gin.Default()` — explicitly attach only the middleware you need.
- Keep handler functions thin: parse input, call a service, marshal output. All business logic lives in the service layer.
- Define request and response structs per endpoint; never reuse domain model structs directly as API DTOs.
- Group related endpoints with `router.Group("/api/v1/users")` and attach group-scoped middleware at the group level.

## Routing & Middleware

- Register middleware with `router.Use(...)` for global concerns and `group.Use(...)` for scoped concerns.
- Write middleware as `func() gin.HandlerFunc` factory functions that close over their configuration.
- Abort early in middleware with `c.AbortWithStatusJSON(code, gin.H{"error": msg})` — never call `c.Next()` after aborting.
- Use `c.Set("userID", id)` and `c.MustGet("userID").(string)` for passing typed values through middleware.
- Register a custom recovery middleware that logs stack traces and returns a consistent JSON 500 body.

## Request Binding & Validation

- Use `c.ShouldBindJSON(&req)` for JSON bodies; use `c.ShouldBindQuery(&req)` for query params — never mix them.
- Annotate binding structs with `binding:"required,min=1,max=255"` tags using `go-playground/validator`.
- Return binding errors as 400 responses: check `var ve validator.ValidationErrors` with `errors.As` to produce field-level messages.
- Bind URI parameters with `c.ShouldBindUri(&req)` and a struct tagged with `uri:"id"`.
- Validate business-level constraints in the service layer, not in binding tags.

## Error Handling

- Define a typed error response struct and use it exclusively: `type APIError struct { Code string; Message string }`.
- Centralize error mapping in a `handleError(c *gin.Context, err error)` helper that maps domain errors to HTTP status codes.
- Use `errors.Is` / `errors.As` in the error handler to branch on sentinel errors and custom error types.
- Never call `c.JSON` directly in handlers for error cases — always funnel through `handleError`.
- Log errors with `slog` including the request ID retrieved from `c.GetString("requestID")`.

## Context & Dependencies

- Inject dependencies (services, stores, config) via a handler struct initialized once at startup.
- Never use `gin.Context` outside of handler/middleware code — pass extracted values to service functions.
- Store the request context with `c.Request.Context()` when calling downstream services so cancellation propagates.
- Extract the request ID in middleware, attach it to `c.Request.Context()` via a context key, and store it with `c.Set`.

## Security

- Set `gin.SetMode(gin.ReleaseMode)` in production — debug mode leaks route information.
- Use `c.ClientIP()` carefully — configure trusted proxies with `router.SetTrustedProxies([]string{...})`.
- Sanitize all path parameters before using them in SQL queries or filesystem operations.
- Add security headers in a middleware: `X-Content-Type-Options`, `X-Frame-Options`, `Strict-Transport-Security`.

## Testing

- Use `httptest.NewRecorder()` with `router.ServeHTTP(w, req)` — do not start a live server in unit tests.
- Initialize a test router with `gin.New()` and only the middleware under test to keep tests isolated.
- Use table-driven tests with subtests (`t.Run`) for handlers with multiple input/output scenarios.
- Mock service interfaces injected into handlers; assert both the HTTP status code and the JSON response body.

## Performance

- Use `c.Stream` for large or chunked responses instead of buffering the entire payload in memory.
- Pre-warm the router by calling `router.Routes()` once at startup to force trie compilation.
- Set explicit read/write timeouts on the underlying `http.Server` wrapping the Gin engine.
- Avoid allocating inside hot middleware (auth, logging) — pre-allocate buffers or use `sync.Pool`.

# Go gRPC Agent Rules

> Applies to the service-to-service gRPC API (`proto/`, `internal/grpcserver`, `internal/grpcclient`); see also "gRPC (Service-to-Service)" in the project conventions above. The general Go practices here (godoc on exported identifiers, no global state, `context.Context` first, early returns, `%w` wrapping, never exposing internal errors) apply to the REST code as well.

## Project Context

You are building Go microservices with gRPC, Protocol Buffers, and the buf toolchain. The service exposes typed RPC methods with strong contracts, interceptors for cross-cutting concerns, and support for unary and streaming RPCs.

## Code Style & Structure

- Run `go fmt`, `go vet`, and `golangci-lint` before every commit.
- Write godoc comments for all exported types and functions, starting with the identifier name.
- Use table-driven tests and name each test case descriptively.
- Avoid global state; pass dependencies explicitly via constructors or function parameters.
- Keep service implementation files under 300 lines; extract helpers into separate files within the same package.

## Proto Definitions

- Define all APIs in `.proto` files first — treat them as the immutable API contract.
- Run `buf lint` to enforce style rules: package naming, field casing, service naming conventions.
- Run `buf breaking` locally (`make proto-breaking`) before merging to catch backward-incompatible changes before they reach main.
- Version packages with `package myservice.v1;` — never break an existing version.
- Use well-known types: `google.protobuf.Timestamp` for times, `google.protobuf.FieldMask` for partial updates, `google.protobuf.Duration` for durations.
- Write comments on every service, RPC, message, and field — they become API documentation for `buf.build`.
- Commit generated code (`gen/`) together with the `.proto` change that produced it; never generate it during the Docker build.

## Service Implementation

- Embed `UnimplementedXxxServer` in the service struct for forward compatibility with new RPC methods.
- Accept `context.Context` as the first parameter in all business logic functions called from handlers.
- Map protobuf types to internal domain models at the handler boundary — do not leak proto types into business logic packages.
- Inject repositories, clients, and configuration via the server struct constructor.
- Return early on errors; keep the happy path at the leftmost indentation level.
- Validate all request fields at the entry point of each RPC — return `codes.InvalidArgument` for missing or malformed inputs.

## Interceptors

- Chain interceptors with `grpc.ChainUnaryInterceptor(...)` and `grpc.ChainStreamInterceptor(...)`.
- Register interceptors in order from outermost to innermost: recovery, logging, metrics, auth, validation.
- Implement a recovery interceptor that catches panics and converts them to `status.Error(codes.Internal, ...)`.
- Add a logging interceptor that records method name, duration, status code, and request ID for every RPC call.
- Implement an auth interceptor that extracts bearer tokens from `metadata.FromIncomingContext(ctx)` and validates them.

## Streaming RPCs

- Use server streaming for large result sets, real-time feeds, and progress reports.
- Use client streaming for bulk ingestion and aggregation of multiple messages.
- Use bidirectional streaming for interactive protocols, collaborative sessions, and live dashboards.
- Check `ctx.Err()` at the top of stream processing loops to handle cancellation and deadline exceeded gracefully.
- Configure `keepalive.ServerParameters` with a sensible `Time` and `Timeout` for long-lived stream connections.
- Set `MaxRecvMsgSize` and `MaxSendMsgSize` on the server for RPCs that transfer large payloads.

## Error Handling

- Use `status.Errorf(codes.X, "message")` for all gRPC errors — never return raw Go errors from RPC handlers.
- Map domain errors consistently: `NotFound` for missing resources, `AlreadyExists` for duplicates, `InvalidArgument` for bad input, `PermissionDenied` for authorization failures, `Internal` for unexpected errors.
- Use `status.WithDetails(pb)` to attach structured error metadata from `google.golang.org/grpc/status/errdetails`.
- Wrap internal errors with `fmt.Errorf("operation: %w", err)` for context; convert to gRPC status at the handler boundary.
- Never expose SQL errors, file paths, or internal stack traces in client-facing gRPC error messages.

## Health Checking & Operations

- Implement `grpc_health_v1.HealthServer` and register it alongside your service for load balancer health checks.
- Set service health to `SERVING` after all dependencies are ready; set to `NOT_SERVING` during graceful shutdown.
- Implement graceful shutdown: call `server.GracefulStop()`, wait for in-flight RPCs, then `server.Stop()` with a deadline.
- Register `reflection.Register(server)` in non-production environments to support `grpcurl` and `grpcui`.

## Testing

- Use `google.golang.org/grpc/test/bufconn` for in-process gRPC testing without opening OS network ports.
- Write a `testServer(t)` helper that starts the server on a `bufconn` listener and returns a connected client.
- Test each RPC for: success, `InvalidArgument`, `NotFound`, `PermissionDenied`, and context cancellation cases.
- Test interceptors independently by wrapping a minimal handler and asserting on metadata and returned status.
- Run `buf lint` and `buf breaking` locally (`make proto`, `make proto-breaking`) before every merge that touches `proto/`.
