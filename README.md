# ATLAS Backend Core

The ATLAS backend: a **REST API** (Gin) for the frontend and **gRPC** for communication between backends, running in one process and sharing the same services. Data lives in PostgreSQL via GORM, with versioned SQL migrations and Docker for both development and production.
Coding and architecture rules are in [docs/GUIDELINES.MD](docs/GUIDELINES.MD). Read the **ATLAS Backend — Project Conventions** section before adding a feature.

## Stack

| Area | Choice |
| --- | --- |
| Language | Go 1.26 (`go.mod` downloads the toolchain automatically if your local Go is older) |
| HTTP (frontend) | Gin with custom middleware (request ID, access log, recovery, security headers, CORS, body limit) |
| gRPC (between backends) | grpc-go + buf, interceptors (request ID, recovery, logging, auth token), health check |
| Database | PostgreSQL 17 + GORM (pgx driver) |
| Migrations | golang-migrate, SQL files in `migrations/` embedded into the binaries |
| Logging | `log/slog` (text in dev, JSON in prod), `request_id` attached to every log line |
| Containers | Multi-stage Dockerfile (`deps`, `dev`, `build`, `prod`), same pattern as the frontend Docker guide |

## Docker

The setup follows the same pattern as the frontend Docker guide:
- **One Dockerfile, two modes**: `compose.dev.yaml` builds the `dev` target (hot reload with air), and `compose.prod.yaml` builds the `prod` target (distroless, non-root, binaries only).
- **Dev and prod can run side by side**: each compose file has its own `name` (`atlas-backend-dev`, `atlas-backend-prod`), so containers, networks, and volumes are separate.
- **Always rebuilt**: `pull_policy: build` rebuilds the image on every `up`; unchanged steps come from the cache.
- **One `.env` for every mode**: read by dev, prod, and `go run`. It never ends up in an image.
- **Configuration is read at runtime**: unlike the frontend, which bakes `VITE_*` values in at build time, one backend image works for every environment.

Requires Docker Desktop, or Docker Engine with Compose v2.24+.

### Build and run

Copy `.env.example` to `.env` first (`cp .env.example .env`); both modes read it.

| Mode | Start | Stop | REST | gRPC |
| --- | --- | --- | --- | --- |
| Development | `docker compose -f compose.dev.yaml up` | `docker compose -f compose.dev.yaml down` | http://localhost:3333 | `localhost:9090` |
| Production | `docker compose -f compose.prod.yaml up -d` | `docker compose -f compose.prod.yaml down` | http://localhost:8000 | `localhost:9000` |

Host ports are chosen so they do not clash with the frontend (dev `5173`, prod `8080`). REST in dev uses `3333` to match the default `VITE_BACKEND_URL` in the frontend template.

**Development**
- Only the source folders (`cmd/`, `internal/`, `migrations/`, `gen/`) are mounted. Changes there rebuild the API immediately.
- Changes outside those folders (`go.mod`, `.air.toml`, `Dockerfile`, `.env`) need `down` and then `up` again; the rebuild happens automatically.
- The dev PostgreSQL is reachable from the host at `localhost:5433` (default `atlas`/`atlas`/`atlas`), and migrations run automatically on startup.

**Production**
- `POSTGRES_PASSWORD` and `GRPC_AUTH_TOKEN` (at least 32 characters, e.g. `openssl rand -hex 32`) must be set in `.env`. Compose refuses to start without them.
- Migrations run automatically when the API starts, and the database port is not published to the host.
- Using a managed PostgreSQL? See the comments in [compose.prod.yaml](compose.prod.yaml).

Other common commands:

```bash
# Follow logs
docker compose -f compose.dev.yaml logs -f api
docker compose -f compose.prod.yaml logs -f api

# Dev database only (then run the API on the host with go run ./cmd/api)
docker compose -f compose.dev.yaml up -d db

# Manual migration commands
docker compose -f compose.dev.yaml exec api go run ./cmd/migrate down 1
docker compose -f compose.prod.yaml run --rm --no-deps --entrypoint /app/migrate api version

# Quick checks
curl -i http://localhost:3333/healthz
curl -i http://localhost:3333/readyz

# Build the production image without Compose
docker build --target prod -t atlas-backend:latest .
docker run --rm -p 8000:8080 -p 9000:9090 --env-file .env -e APP_ENV=production atlas-backend:latest
```

The `Makefile` offers the same shortcuts (`make dev`, `make prod-up`, `make help`) for Linux, macOS, and WSL users.

### Running the API without Docker

```bash
cp .env.example .env
docker compose -f compose.dev.yaml up -d db   # PostgreSQL only, port 5433
go run ./cmd/migrate up
go run ./cmd/api                              # REST :3333, gRPC :9090
```

Want to use a locally installed PostgreSQL (port 5432)? Change `DATABASE_URL` in `.env`.

## REST (frontend)

```bash
curl -X POST localhost:3333/api/v1/users -H "Content-Type: application/json" \
     -d '{"name":"Ada Lovelace","email":"ada@example.com"}'
curl "localhost:3333/api/v1/users?page=1&page_size=20"
```

Response format:

```jsonc
// success (single resource)
{ "data": { "id": "…", "name": "…" } }

// success (list)
{ "data": [ … ], "meta": { "page": 1, "page_size": 20, "total": 42, "total_pages": 3 } }

// error
{ "error": { "code": "VALIDATION_ERROR", "message": "request validation failed",
             "details": [ { "field": "email", "message": "must be a valid email address" } ] } }
```

Every response carries an `X-Request-ID` header. Include it when reporting a bug so the related logs are easy to find.

## gRPC (between backends)

The frontend only uses REST. gRPC is how other backends call ATLAS, and how ATLAS calls other backends.

- **Contract**: [proto/atlas/user/v1/user.proto](proto/atlas/user/v1/user.proto). Share the `.proto` files with the other backend teams so they can generate their own clients.
- **Auth**: every call must send `authorization: Bearer <GRPC_AUTH_TOKEN>` metadata. In development the token may be left empty (auth disabled).
- **Request ID**: `x-request-id` metadata is forwarded between services, so logs can be traced across backends.
- **Health & reflection**: `grpc.health.v1.Health` is callable without a token. Reflection is only enabled outside production.

Try it with [grpcurl](https://github.com/fullstorydev/grpcurl) in development mode:

```bash
grpcurl -plaintext localhost:9090 list
grpcurl -plaintext localhost:9090 grpc.health.v1.Health/Check
grpcurl -plaintext -d '{"name":"Ada","email":"ada@example.com"}' \
  localhost:9090 atlas.user.v1.UserService/CreateUser
```

After changing a `.proto`, regenerate the code and commit `proto/` and `gen/` together (`make proto`):

```bash
go run github.com/bufbuild/buf/cmd/buf@v1.75.0 lint
go run github.com/bufbuild/buf/cmd/buf@v1.75.0 generate
```

Calling another backend: open a connection with `grpcclient.New(grpcclient.Config{Target: "payments:9090", AuthToken: ..., Timeout: 5 * time.Second})` and wrap it in the client generated from that backend's proto. The request ID and token are sent automatically. Details are in [docs/GUIDELINES.MD](docs/GUIDELINES.MD).

## Migrations

```bash
go run ./cmd/migrate create create_posts_table   # create a new up/down pair
go run ./cmd/migrate up                          # apply pending migrations
go run ./cmd/migrate down 1                      # roll back one step
go run ./cmd/migrate version                     # show the current schema version
go run ./cmd/migrate force 3                     # fix a "dirty" state
```

The schema is changed only through migrations; GORM `AutoMigrate` is not used.

## Project Layout

```
cmd/api              entrypoint (REST + gRPC in one process)
cmd/migrate          migration CLI
proto/               gRPC contracts (.proto)
gen/                 code generated by buf (do not edit by hand)
internal/config      configuration from env + validation
internal/router      InitRouter: global middleware & route groups
internal/middleware  request ID, logger, recovery, security headers, CORS, body limit
internal/handler     thin REST handlers + DTOs + handleError
internal/grpcserver  gRPC server, interceptors, RPC handlers
internal/grpcclient  connections to other backends' gRPC servers
internal/service     business logic (shared by REST & gRPC)
internal/repository  data access (GORM)
internal/domain      entities / models
internal/apperror    domain errors (NotFound, Conflict, ...)
internal/httpx       REST response envelope & error format
internal/validation  validation messages (REST & gRPC)
internal/database    GORM connection, Transactor, migration runner
migrations/          SQL migration files
```

The `users` module is a complete example (migration → domain → repository → service → REST handler + gRPC RPC → tests) to use as a template for new features.

## Configuration

Every variable, with its default and an explanation, is listed in [.env.example](.env.example). Only `DATABASE_URL` is required (for `go run`; in Docker the compose files set it).
Invalid configuration makes the application fail at startup with a message naming the offending variable.

## Testing & Lint

There is no CI/CD; run these checks locally before merging:

```bash
go test ./...                 # unit tests, including gRPC via bufconn (no database needed)
golangci-lint run             # linter (config in .golangci.yml)
docker build --target prod .  # make sure the production image still builds
```
