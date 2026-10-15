# Deployment

The app is one Go binary with SQLite inside. It runs over plain HTTP behind the
operator's existing reverse proxy; TLS is the operator's job, never the app's
(see [`docs/adr/0001-host-agnostic-exposure.md`](../docs/adr/0001-host-agnostic-exposure.md)).

Artifacts:

| File | Purpose |
|---|---|
| [`Dockerfile`](../Dockerfile) | Multi-stage build; static binary, `CGO_ENABLED=0`, runs unprivileged. |
| [`docker-compose.yml`](../docker-compose.yml) | Compose service pulling the published image, with a named volume for SQLite. |
| [`.env.example`](../.env.example) | Copy to `.env` and set `TOKEN`. |
| [`nginx.conf`](nginx.conf) | Example nginx `server` block (plain HTTP). |

## Configuration

All configuration is environment variables.

| Variable | Default | Meaning |
|---|---|---|
| `PORT` | `8080` | TCP port the app listens on. |
| `BASE_URL` | *(derived)* | Public base URL for generated links. Optional: by default the app derives it from each request (`Host` plus `X-Forwarded-Proto`). Set it only when the proxy presents a different public address than the request `Host`. |
| `DB_PATH` | `bookmarks.sqlite` | Path to the SQLite file. The image sets `/data/bookmarks.sqlite`. |
| `TOKEN` | *(required)* | Shared secret. Authentication is **on by default**, so a non-empty value is required. The app exits at startup if `TOKEN` is empty and auth is not disabled. |
| `AUTH_DISABLED` | *(off)* | Disables authentication on a fully trusted network. Accepts `1`, `true`, `yes`, or `on`. |

`TOKEN` is accepted as `Authorization: Bearer <token>`, or as a session cookie
set after entering it once. The cookie does **not** carry the Token: it holds a
value derived from it, so copying the cookie out of a browser does not hand over
the operator's long-lived secret, and rotating `TOKEN` retires every cookie
issued under the old one. `BASE_URL` is optional per ADR-0001.

Auth is on by default and the app **fails fast**: with `TOKEN` unset and
`AUTH_DISABLED` off, the process logs the reason and exits, so a misconfigured
container does not silently serve every Bookmark in the clear.

### Choose a long `TOKEN`

`POST /login` is not rate limited: the app is a single-user Start page, and a
limiter would have to be built and tuned to be worth having. What keeps guessing
impractical is the Token's own length, so **use a long random one** — 32
characters or more from a password manager, or:

```sh
head -c 32 /dev/urandom | base64     # a suitable TOKEN
```

A short or guessable Token is the one configuration this deployment cannot
protect you from. Comparison is constant-time, so a wrong Token leaks nothing
through timing, and the login page returns the same redirect for every wrong
value.

## Run with Docker Compose

```sh
cp .env.example .env      # then edit TOKEN
docker compose up -d
```

The app is then at <http://localhost:8080/>. Compose pulls
`ghcr.io/afrieirham/bookmark-manager:latest`, which is built for `linux/amd64`
and `linux/arm64`, so it runs on a VPS or an ARM box such as a Raspberry Pi
without a local build.

The SQLite file lives in the `bookmarks-data` named volume and survives
`docker compose down`. Backups are the operator's job (the spec has no
app-level backup); `docker compose exec bookmarks cp /data/bookmarks.sqlite`
followed by `docker cp` is enough to lift a copy out.

### Storage: named volume or bind mount

The default compose file uses a **named volume**, which needs no setup: the
image hands `/data` to uid `65532`, and a named volume inherits that ownership.

A **bind mount** (`- ./data:/data`) works too, and is the right choice when you
want the file somewhere you already back up. A bind mount is judged by the host
directory's own ownership, not the image's, so hand it to the container's uid
first:

```sh
sudo mkdir -p ./data
sudo chown -R 65532:65532 ./data
```

Without that the container starts and then fails to open the SQLite file. To run
as a different uid entirely, override `user:` in the compose service.

The image runs unprivileged by default. Nothing in it needs root.

### Build from source instead

To run a checkout rather than the published image, comment out `image:` and
uncomment `build:` in `docker-compose.yml`, then `docker compose up --build`.

## Put it behind a reverse proxy

Install [`nginx.conf`](nginx.conf) as a server block (e.g.
`/etc/nginx/conf.d/bookmarks.conf`) on the host running the compose stack, after
replacing `server_name` with your own name, then:

```sh
sudo nginx -t && sudo systemctl reload nginx
```

nginx proxies to `127.0.0.1:8080` and sets `X-Forwarded-Proto`, which the app
reads to build correct links. Adding TLS to that block — or terminating it
anywhere upstream and forwarding the scheme — is the only change needed for
HTTPS: PWA install and Android sharing switch on by themselves, with no app
change. Any other reverse proxy (Caddy, Traefik, and so on) works the same way
as long as it passes the real `Host` and sets `X-Forwarded-Proto`.

## Health check

`GET /healthz` answers `200` when the store can be read and `503` when it
cannot, so a restart loop or a wedged volume shows up as unhealthy rather than
as silent breakage. It is served without the Token, so a probe needs no secret
in its configuration. The compose service wires it in as a `healthcheck`.

## Verifying a deployment (human checklist)

1. `docker compose up -d` starts the container, and `docker compose ps` reports
   it as `healthy` within a minute.
2. `curl -fsS http://localhost:8080/healthz` prints `ok`; the same request
   without any credentials also succeeds.
3. `curl -I http://localhost:8080/` redirects to `/login` rather than showing
   the Start page: auth is on.
4. Save a Bookmark, then `docker compose restart bookmarks`: the Bookmark is
   still there.
5. `nginx -t` passes with the server block, and a request through the proxy
   reaches the app.
6. Links on the Start page use the public name, not `127.0.0.1` — if they don't,
   the proxy is not passing `Host` through.
7. The `<title>` fetcher can reach an https site (CA certificates are present in
   the runtime image), so a newly saved URL picks up a Title.
8. Over HTTPS, the browser offers to install the app and Android sharing targets
   it.
