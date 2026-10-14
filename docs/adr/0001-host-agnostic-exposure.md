# Host-agnostic exposure, TLS left to the operator

The app must run behind whatever the operator already has, so it never assumes a host, proxy, or certificate. It builds its links from the incoming request, with a `BASE_URL` environment variable as the override when a reverse proxy presents a different public address.

The MVP ships without TLS: it runs over plain HTTP on a private LAN + VPN. The PWA manifest and `share_target` ship in the code but stay dormant until the operator serves the app over HTTPS with a certificate the device trusts — a secure context requires trust, and a self-signed certificate does not count. PWA install and the Android share target therefore activate only once TLS is present. The app itself never handles certificates.

## Considered options

- **Tailscale Serve** (trusted `*.ts.net`) — rejected as the app's stance: it is one operator's choice, not something to bake in.
- **Let's Encrypt DNS-01 + split-horizon DNS** and **mkcert local CA** — valid operator paths, documented, not required.
- **Hard-requiring trusted HTTPS in the MVP** — rejected: it adds setup friction and blocks hosts without a domain.

## Consequences

- The same binary works on a LAN, a VPS, or a tailnet; only `BASE_URL` may need setting.
- Capture degrades gracefully: without trusted HTTPS, fall back to the paste box, the bookmarklet, and the iOS Shortcut.
- Authentication tokens travel in cleartext over plain HTTP on the LAN (see the Authentication decision).

Status: accepted
