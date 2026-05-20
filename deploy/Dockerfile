# syntax=docker/dockerfile:1.7

ARG VERSION=0.0.0-dev
ARG NODE_IMAGE=node:20-bookworm-slim
ARG GO_IMAGE=golang:1.26.2-bookworm
ARG RUNTIME_IMAGE=debian:bookworm-slim

FROM --platform=$BUILDPLATFORM ${NODE_IMAGE} AS web-deps

WORKDIR /app/web
ENV CI=1

COPY web/package.json web/package-lock.json* ./
RUN --mount=type=cache,target=/root/.npm,sharing=locked \
    (test -f package-lock.json && npm ci || npm install --no-audit --no-fund)


FROM web-deps AS web-build

COPY web ./
ARG VERSION=0.0.0-dev
ARG BUILD_NODE_OPTIONS=--max-old-space-size=1024
ENV VITE_APP_VERSION=${VERSION} \
    NODE_OPTIONS=${BUILD_NODE_OPTIONS}
RUN npm run build


FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS go-build

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked go mod download

COPY internal ./internal
COPY --from=web-build /app/internal/web/dist ./internal/web/dist
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=0.0.0-dev
ARG BUILD_GOMAXPROCS=2
ARG BUILD_GOMEMLIMIT=2GiB
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    GOMAXPROCS=${BUILD_GOMAXPROCS} GOMEMLIMIT=${BUILD_GOMEMLIMIT} \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -p=${BUILD_GOMAXPROCS} -trimpath -tags=embed \
    -ldflags="-s -w -X chatgpt2api/internal/version.Version=${VERSION}" \
    -o /out/chatgpt2api ./internal


FROM --platform=$TARGETPLATFORM ${RUNTIME_IMAGE} AS app

WORKDIR /app
ENV PORT=80
ENV CHATGPT2API_DEPLOYMENT=docker

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    git \
    tzdata \
    && rm -rf /var/lib/apt/lists/*

COPY --from=go-build /out/chatgpt2api ./chatgpt2api

RUN mkdir -p /app/data && chmod +x /app/chatgpt2api

EXPOSE 80

HEALTHCHECK --interval=30s --timeout=10s --start-period=10s --retries=3 \
    CMD curl -fsS http://127.0.0.1:${PORT:-80}/health || exit 1

CMD ["/app/chatgpt2api"]
