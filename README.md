# Bookmark Manager

A self-hosted, single-user start page: one Go binary with SQLite inside.

![MIT](https://img.shields.io/badge/license-MIT-blue)
![Go](https://img.shields.io/badge/Go-1.26-00ADD8)

Keep a grid of saved URLs and open them from any device. There is no account, no
sync service and no telemetry: the app talks to nothing but the pages you save,
and only to read their `<title>`. All of its CSS is inline, so the page loads
nothing from a CDN either.

## What it does

- **A start page.** Your saved URLs as a grid, at the app root.
- **Several ways to save.** A paste box on the page, a bare-URL alias
  (`/example.com/deep/page` saves that URL), a draggable bookmarklet, and an
  Android share target, so a link reaches the app straight from the share sheet.
- **Titles for free.** On save the app fetches the page's `<title>`, falling
  back to the host name when it cannot.
- **One shared secret.** Authentication is on by default, and the app refuses to
  start without a `TOKEN` rather than serving your bookmarks in the clear.
- **Installable.** Served over HTTPS it installs as a PWA, which is also what
  turns the share target on.
- **One binary, no database server.** SQLite is embedded, so all state is a
  single file you can copy to back up.
- **Multi-arch.** The published image runs on `linux/amd64` and `linux/arm64`.

## Quick start

Pick a Token first — you will be asked for it once per device:

```sh
TOKEN=$(head -c 32 /dev/urandom | base64)
echo "$TOKEN"          # keep this somewhere safe
```

Run it:

```sh
docker run -d --name bookmarks \
  -p 8080:8080 \
  -e TOKEN="$TOKEN" \
  -v bookmarks-data:/data \
  ghcr.io/afrieirham/bookmark-manager:latest
```

Then open <http://localhost:8080/> and enter the Token.

### With Compose

```sh
git clone https://github.com/afrieirham/bookmark-manager
cd bookmark-manager
cp .env.example .env      # set TOKEN
docker compose up -d
```

The compose file pulls the published image; comment out `image:` and uncomment
`build:` in it to build from your checkout instead.

## Configuration

Everything is environment variables; there is no config file.

| Variable | Default | Meaning |
|---|---|---|
| `PORT` | `8080` | Port the app listens on. |
| `DB_PATH` | `bookmarks.sqlite` | Path to the SQLite file. |
| `TOKEN` | *(required)* | The shared secret. The app exits if it is unset. |
| `BASE_URL` | *(derived)* | Public base URL for generated links. Optional; see below. |
| `AUTH_DISABLED` | *(off)* | Disables authentication on a fully trusted network. |

`BASE_URL` normally needs no setting: the app derives it from each request, so
it is already correct behind a proxy that passes the real `Host` through. Set it
only when the proxy presents a different public address than the request `Host`.

`AUTH_DISABLED=1` serves every bookmark to anyone who can reach the port. Use it
only on a network you fully trust.

[`deploy/README.md`](deploy/README.md) covers storage, reverse proxies, and the
health endpoint in more detail.

## HTTPS and reverse proxies

The app speaks plain HTTP and never touches certificates; TLS is yours to
terminate, wherever you already do that. It reads `X-Forwarded-Proto` to build
correct links, so an existing proxy that sets it (nginx, Caddy, Traefik) needs
no app-side configuration. [`deploy/nginx.conf`](deploy/nginx.conf) is a
starting point.

Two features need a trusted certificate to switch on, because a browser only
grants them in a secure context: installing the PWA, and the Android share
target. Both activate on their own once the app is served over HTTPS — a
self-signed certificate is not enough.

## Health check

`GET /healthz` returns `200` when the app can read its store and `503` when it
cannot, so a bad volume shows up as unhealthy rather than as silent breakage. It
is served without the Token, so a probe needs no credentials.

## Development

Requires Go 1.26 or newer. Docker is not needed to build or test.

```sh
go test ./...
go run .            # needs TOKEN set, or AUTH_DISABLED=1
```

The binary is fully static (`CGO_ENABLED=0`) — the SQLite driver is pure Go — so
`go build` cross-compiles to any platform without a toolchain for it:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bookmarks .
```

## License

[MIT](LICENSE).
