# syntax=docker/dockerfile:1

# ---------------------------------------------------------------------------
# build stage
# ---------------------------------------------------------------------------
# Pinned to $BUILDPLATFORM so buildx runs this stage on the host's own
# architecture and cross-compiles, rather than emulating the target under QEMU.
# That works because modernc.org/sqlite is a pure-Go driver (below), so GOARCH
# is the only thing that has to change: an arm64 image builds in seconds instead
# of minutes. go.mod declares `go 1.26.0`, so the builder must be 1.26 or newer
# or the build tries to download a toolchain.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src

ARG TARGETOS
ARG TARGETARCH

# Download modules first so this layer is cached across source edits.
COPY go.mod go.sum ./
RUN go mod download

# The frontend (templates) is embedded via go:embed, so a plain source copy is
# all the build needs.
COPY . .

# CGO_ENABLED=0 keeps the binary fully static and needs no libc in the runtime
# image, which is what lets the runtime stage stay tiny.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/bookmarks .

# ---------------------------------------------------------------------------
# runtime stage
# ---------------------------------------------------------------------------
# A supported Alpine release: 3.20 stopped receiving security updates in April
# 2026, so a published image must not sit on it.
FROM alpine:3.24

# CA certificates so the page-<title> fetcher can reach https sites. The busybox
# wget is deliberately kept: the compose healthcheck uses it.
RUN apk add --no-cache ca-certificates

COPY --from=build /out/bookmarks /usr/local/bin/bookmarks

# The SQLite file lives on a mounted volume. /data is created and handed to the
# unprivileged user here so a named volume — which inherits this directory's
# ownership — works out of the box.
RUN mkdir -p /data && chown 65532:65532 /data
VOLUME ["/data"]

# Run unprivileged. The binary only ever reads and writes its own SQLite file,
# so nothing here needs root. A bind mount is judged by the host directory's own
# ownership, so an operator mounting one must hand that directory to 65532 —
# see deploy/README.md. To run as a different uid, override `user:` in compose.
USER 65532:65532

EXPOSE 8080

# Defaults match main.go. docker-compose.yml sets these explicitly too.
ENV PORT=8080 \
    DB_PATH=/data/bookmarks.sqlite

ENTRYPOINT ["/usr/local/bin/bookmarks"]
