# Bookmarklet server (PROTOTYPE — throwaway)

A tiny real server so you can test the bookmarklet against live web pages.
**Not** the production app: JSON-file storage, no auth, no TLS.

## Run

    go run .            # listens on :8080 (PORT=xxxx to change)

No Go installed? Use Docker:

    docker run --rm -p 8080:8080 -v "$PWD":/app -w /app golang:1.24 go run .

## Use

1. Open http://localhost:8080/
2. Drag **★ Save to app** to your bookmarks bar.
3. Go to any page and click it. A new tab opens the app, saves the page, and asks you to rename.

## Notes

- Clicking from an HTTPS page opens `http://localhost:8080/...` in a new tab. That top-level
  navigation is allowed — mixed-content rules block background fetches, not navigations.
- Storage is `bookmarks.json` in this folder. Delete it to start over.
- Works from other devices too: run it on your Pi and open `http://<pi-ip>:8080/`. The bookmarklet
  is generated for whatever host you load the page from (matching the host-agnostic decision).
