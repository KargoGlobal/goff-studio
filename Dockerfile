# syntax=docker/dockerfile:1.7

# ---------- stage 1: build the Vite/React frontend ----------
# The Go binary embeds cmd/goff-studio/dist via //go:embed all:dist, and
# vite.config.ts writes its bundle straight into that directory.
FROM --platform=$BUILDPLATFORM node:24-alpine AS web

WORKDIR /src/web

COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci

COPY web/tsconfig.json web/tsconfig.app.json web/tsconfig.node.json ./
COPY web/vite.config.ts web/index.html ./
COPY web/public ./public
COPY web/src ./src

# `npm run build` is `tsc -b && vite build`; outDir is ../cmd/goff-studio/dist.
RUN npm run build && test -f /src/cmd/goff-studio/dist/index.html

# ---------- stage 2: build the static Go binary ----------
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY cmd ./cmd
COPY internal ./internal

# Overwrite whatever placeholder dist the repo carried with the freshly built one.
COPY --from=web /src/cmd/goff-studio/dist ./cmd/goff-studio/dist

ARG TARGETOS
ARG TARGETARCH

# No -X version stamping: cmd/goff-studio/main.go declares no version/revision
# variables, so -X against them would link silently and do nothing.
ENV CGO_ENABLED=0
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/goff-studio \
      ./cmd/goff-studio

# ---------- stage 3: distroless runtime ----------
FROM gcr.io/distroless/static-debian12:nonroot

LABEL org.opencontainers.image.title="GO Feature Flag Studio" \
      org.opencontainers.image.description="Git-backed admin UI for GO Feature Flag" \
      org.opencontainers.image.source="https://github.com/go-feature-flag/studio" \
      org.opencontainers.image.licenses="MIT"

COPY --from=build /out/goff-studio /usr/local/bin/goff-studio

# nonroot is uid/gid 65532 in the distroless base.
USER nonroot:nonroot

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/goff-studio"]

# No -config flag, so a mounted file at this path is used and a missing one falls
# back to GOFF_STUDIO_* variables instead of failing.
WORKDIR /etc/goff-studio
