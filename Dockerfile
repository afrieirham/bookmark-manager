# syntax=docker/dockerfile:1

# ---------------------------------------------------------------------------
# build stage
# ---------------------------------------------------------------------------
# go.mod declares `go 1.26.0`, so the builder must be 1.26 or newer; otherwise
# the image build tries to download a toolchain at build time.
FROM golang:1.26-alpine AS build
WORKDIR /src

# Download modules first so this layer is cached across source edits.
COPY go.mod go.sum ./
RUN go mod download

# The frontend (templates) is embedded via go:embed, so a plain source copy is
# all the build needs.
COPY . .

# modernc.org/sqlite is a pure-Go driver, so CGO is not required: this produces
# a fully static binary that needs no libc in the runtime image.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/bookmarks .

# ---------------------------------------------------------------------------
# runtime stage
# ---------------------------------------------------------------------------
FROM alpine:3.20

# CA certificates so the page-<title> fetcher can reach https sites.
RUN apk add --no-cache ca-certificates

COPY --from=build /out/bookmarks /usr/local/bin/bookmarks

# The SQLite file lives on a mounted volume. Declaring /data creates the path in
# the image; docker-compose bind-mounts the host directory over it.
VOLUME ["/data"]
EXPOSE 8080

# Defaults match main.go. docker-compose.yml sets these explicitly too.
ENV PORT=8080 \
    DB_PATH=/data/bookmarks.sqlite

# Runs as root so it can write the bind-mounted host directory out of the box.
# To harden: `chown -R 65532:65532 /mnt/usb1/bookmarks` and add
# `user: "65532:65532"` to the compose service. See deploy/README.md.
ENTRYPOINT ["/usr/local/bin/bookmarks"]
