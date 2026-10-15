# Deployment

The app is one Go binary with SQLite inside. It runs over plain HTTP behind the
operator's existing reverse proxy; TLS is the operator's job, never the app's
(see [`docs/adr/0001-host-agnostic-exposure.md`](../docs/adr/0001-host-agnostic-exposure.md)).

Artifacts:

| File | Purpose |
|---|---|
| [`Dockerfile`](../Dockerfile) | Multi-stage build; static binary, `CGO_ENABLED=0`. |
| [`docker-compose.yml`](../docker-compose.yml) | Compose service with SQLite bind-mounted. |
| [`.env.example`](../.env.example) | Copy to `.env` and set `TOKEN`. |
| [`nginx.conf`](nginx.conf) | nginx `server` block for `bookmarks.pi` (plain HTTP). |

## Configuration

All configuration is environment variables.

| Variable | Default | Meaning |
|---|---|---|
| `PORT` | `8080` | TCP port the app listens on. |
| `BASE_URL` | *(derived)* | Public base URL for generated links. Optional: by default the app derives it from each request (`Host` plus `X-Forwarded-Proto`). Set it only when the proxy presents a different public address than the request `Host`. |
| `DB_PATH` | `bookmarks.sqlite` | Path to the SQLite file. In the container the compose example sets `/data/bookmarks.sqlite`. |
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
cp .env.example .env                 # then edit TOKEN
sudo mkdir -p /mnt/usb1/bookmarks    # the SQLite bind-mount target
docker compose up --build
```

The app is then at <http://localhost:8080/>. The SQLite file appears at
`/mnt/usb1/bookmarks/bookmarks.sqlite` and survives restarts. Backups are the
operator's job (the spec has no app-level backup).

The image runs as root so it can write the bind-mounted host directory out of
the box. To run it unprivileged instead:

```sh
sudo chown -R 65532:65532 /mnt/usb1/bookmarks
# then add `user: "65532:65532"` to the compose service
```

## Put it behind nginx

Install [`nginx.conf`](nginx.conf) as a server block (e.g.
`/etc/nginx/conf.d/bookmarks.conf`) on the host running the compose stack, then:

```sh
sudo nginx -t && sudo systemctl reload nginx
```

`bookmarks.pi` resolves over the Tailscale subnet route. nginx proxies to
`127.0.0.1:8080` and sets `X-Forwarded-Proto`, which the app reads to build
correct links. If the operator later terminates TLS elsewhere, `$scheme`
becomes `https` and PWA install / Android sharing switch on by themselves — no
app change.

## Verifying a deployment (human checklist)

Docker and nginx are **not installed on the development machine**, so the
Dockerfile, compose file, and nginx block here were **not executed** during
development. A human must confirm:

1. `docker compose up --build` starts the container; `curl -I http://localhost:8080/` returns `200`.
2. `docker build` completes with `CGO_ENABLED=0` (confirms the pure-Go SQLite driver needs no gcc).
3. The produced binary is static: `ldd` reports "not a dynamic executable".
4. After a save, `/mnt/usb1/bookmarks/bookmarks.sqlite` exists; restarting the container keeps the data.
5. `nginx -t` passes with the server block, and `http://bookmarks.pi/` reaches the app.
6. `X-Forwarded-Proto` reaches the app so links use the right scheme.
7. The `<title>` fetcher can reach an https site (CA certificates present in the runtime image).
