# syntax=docker/dockerfile:1

# One Dockerfile for both modes: compose.dev.yaml builds the "dev" target,
# compose.prod.yaml the "prod" target

ARG GO_VERSION=1.26

# Dependencies, shared by every stage below
FROM golang:${GO_VERSION}-alpine AS deps
WORKDIR /app
# go.mod pins an exact Go patch release; fetch that toolchain if the image is older
ENV GOTOOLCHAIN=auto
COPY go.mod go.sum ./
RUN go mod download

# Development: air rebuilds and restarts the API on every source change
FROM deps AS dev
RUN go install github.com/air-verse/air@v1.67.4
COPY . .
# 8080 = REST (frontend), 9090 = gRPC (other backends)
EXPOSE 8080 9090
CMD ["air", "-c", ".air.toml"]

# Build: static binaries for the API and the migration CLI
FROM deps AS build
COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate

# Production: the two binaries only. No Go toolchain, no source code, no shell, non-root.
# Configuration is read at runtime, so the same image works for every environment.
FROM gcr.io/distroless/static-debian12:nonroot AS prod
WORKDIR /app
COPY --from=build /out/api /out/migrate /app/
ENV APP_ENV=production
EXPOSE 8080 9090
USER nonroot:nonroot
ENTRYPOINT ["/app/api"]
