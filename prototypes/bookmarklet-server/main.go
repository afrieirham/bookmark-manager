// PROTOTYPE — throwaway. A tiny real server so the bookmarklet can be tested
// against live web pages. Not the production app: JSON file storage, no auth.
//
// Run:  go run .            (listens on :8080, or PORT=xxxx go run .)
// Open: http://localhost:8080/   then drag "Save to app" to your bookmarks bar.
package main

import (
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
)

type Bookmark struct {
	ID    int    `json:"id"`
	URL   string `json:"url"`
	Title string `json:"title"`
}

type Store struct {
	mu        sync.Mutex
	Bookmarks []Bookmark `json:"bookmarks"`
	NextID    int        `json:"nextId"`
	path      string
}

func loadStore(path string) *Store {
	s := &Store{NextID: 1, path: path}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, s)
	}
	if s.NextID < 1 {
		s.NextID = 1
	}
	return s
}

func (s *Store) persist() {
	b, _ := json.MarshalIndent(s, "", "  ")
	_ = os.WriteFile(s.path, b, 0o644)
}

func normalize(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	u.Fragment = ""
	return u.String()
}

func titleFor(u, title string) string {
	if t := strings.TrimSpace(title); t != "" {
		return t
	}
	if parsed, err := url.Parse(u); err == nil && parsed.Host != "" {
		return strings.TrimPrefix(parsed.Host, "www.")
	}
	return u
}

// add returns the bookmark and whether it was a duplicate.
func (s *Store) add(rawURL, title string) (Bookmark, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := normalize(rawURL)
	for _, b := range s.Bookmarks {
		if b.URL == u {
			return b, true
		}
	}
	b := Bookmark{ID: s.NextID, URL: u, Title: titleFor(u, title)}
	s.NextID++
	s.Bookmarks = append(s.Bookmarks, b)
	s.persist()
	return b, false
}

func (s *Store) rename(id int, title string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	title = strings.TrimSpace(title)
	if title == "" {
		return
	}
	for i := range s.Bookmarks {
		if s.Bookmarks[i].ID == id {
			s.Bookmarks[i].Title = title
		}
	}
	s.persist()
}

func (s *Store) remove(id int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.Bookmarks[:0]
	for _, b := range s.Bookmarks {
		if b.ID != id {
			out = append(out, b)
		}
	}
	s.Bookmarks = out
	s.persist()
}

func (s *Store) get(id int) (Bookmark, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.Bookmarks {
		if b.ID == id {
			return b, true
		}
	}
	return Bookmark{}, false
}

func baseURL(r *http.Request) string {
	if v := os.Getenv("BASE_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	}
	return scheme + "://" + r.Host
}

var page = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Bookmarks</title>
<style>
  :root{--bg:#f7f7f5;--ink:#1a1a18;--muted:#6b6b66;--line:#e6e5e1;--accent:#4f46e5;--soft:#eef0ff}
  *{box-sizing:border-box}
  body{margin:0;background:var(--bg);color:var(--ink);font:16px/1.55 ui-sans-serif,system-ui,-apple-system,"Segoe UI",Roboto,sans-serif}
  main{max-width:860px;margin:0 auto;padding:40px 20px 80px}
  h1{font-size:24px;margin:0 0 4px}
  .flag{display:inline-block;font-size:12px;font-weight:600;color:#b45309;background:#fef3c7;border:1px solid #fcd34d;border-radius:999px;padding:2px 10px;margin-bottom:14px}
  .hint{color:var(--muted);font-size:14px}
  .tools{display:flex;gap:12px;align-items:center;margin:16px 0}
  .bmk{display:inline-block;text-decoration:none;font-weight:600;font-size:14px;padding:8px 14px;border-radius:9px;background:var(--soft);border:1px dashed #b9bdf5;color:var(--accent)}
  .paste{display:flex;gap:8px;margin:8px 0 24px}
  .paste input{flex:1;font:inherit;font-size:15px;padding:8px 12px;border:1px solid var(--line);border-radius:9px;background:#fff}
  button{font:inherit;font-size:14px;padding:8px 14px;border-radius:9px;border:1px solid var(--line);background:#fff;cursor:pointer}
  button.primary{background:var(--accent);border-color:var(--accent);color:#fff}
  .pending{background:var(--soft);border:1px solid #c7cbff;border-radius:12px;padding:14px;margin-bottom:18px}
  .pending form{display:inline-flex;gap:8px;margin:8px 6px 0 0}
  .pending input{font:inherit;font-size:15px;font-weight:600;padding:7px 10px;border:1px solid #c7cbff;border-radius:8px;background:#fff;min-width:min(360px,100%)}
  .grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(240px,1fr));gap:12px}
  .card{border:1px solid var(--line);border-radius:10px;padding:12px 13px;background:#fff}
  .card.hot{border-color:var(--accent);box-shadow:0 0 0 3px var(--soft)}
  .card .t{font-weight:600;margin-bottom:3px}
  .card .u{color:var(--muted);font-family:ui-monospace,Menlo,monospace;font-size:12px;word-break:break-all;margin-bottom:8px}
  .card form{display:inline-flex;gap:6px;margin-right:6px}
  .card input{font:inherit;font-size:13px;padding:5px 8px;border:1px solid var(--line);border-radius:7px;max-width:150px}
  .empty{color:var(--muted);font-style:italic}
</style></head><body><main>
<span class="flag">PROTOTYPE — throwaway server</span>
<h1>Bookmarks</h1>
<p class="hint">Drag <strong>Save to app</strong> to your bookmarks bar, then click it on any page.</p>

{{if .Pending}}
<div class="pending">
  <strong>{{if .Dup}}Already saved{{else}}Saved{{end}}</strong>
  <span class="hint">— rename it now, or leave it.</span>
  <form method="post" action="/rename">
    <input type="hidden" name="id" value="{{.Pending.ID}}">
    <input name="title" value="{{.Pending.Title}}" aria-label="Title">
    <button class="primary">Keep name</button>
  </form>
  <form method="post" action="/delete"><input type="hidden" name="id" value="{{.Pending.ID}}"><button>Remove</button></form>
</div>
{{end}}

<div class="tools">
  <a class="bmk" href="{{.Bookmarklet}}" draggable="true">★ Save to app</a>
  <span class="hint">← drag me</span>
</div>

<form class="paste" method="get" action="/save">
  <input name="u" placeholder="…or paste a URL here" autocomplete="off" autofocus>
  <button class="primary">Save</button>
</form>

<div class="grid">
{{range .Bookmarks}}
  <div class="card {{if $.Pending}}{{if eq $.Pending.ID .ID}}hot{{end}}{{end}}">
    <div class="t">{{.Title}}</div>
    <div class="u">{{.URL}}</div>
    <form method="post" action="/rename"><input type="hidden" name="id" value="{{.ID}}"><input name="title" value="{{.Title}}" aria-label="Title"><button>Rename</button></form>
    <form method="post" action="/delete"><input type="hidden" name="id" value="{{.ID}}"><button>Remove</button></form>
  </div>
{{else}}
  <div class="empty">No bookmarks yet.</div>
{{end}}
</div>
</main></body></html>`))

func main() {
	store := loadStore("bookmarks.json")
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		data := struct {
			Bookmarks   []Bookmark
			Pending     *Bookmark
			Dup         bool
			Bookmarklet template.URL
		}{Bookmarks: store.Bookmarks}

		if idStr := r.URL.Query().Get("saved"); idStr != "" {
			if id, err := strconv.Atoi(idStr); err == nil {
				if b, ok := store.get(id); ok {
					data.Pending = &b
					data.Dup = r.URL.Query().Get("dup") == "1"
				}
			}
		}

		base := baseURL(r)
		js := "javascript:(function(){window.open('" + base +
			"/save?u='+encodeURIComponent(location.href)+'&title='+encodeURIComponent(document.title),'_blank')})()"
		data.Bookmarklet = template.URL(js)

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = page.Execute(w, data)
	})

	http.HandleFunc("/save", func(w http.ResponseWriter, r *http.Request) {
		u := r.URL.Query().Get("u")
		if u == "" {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		b, dup := store.add(u, r.URL.Query().Get("title"))
		target := "/?saved=" + strconv.Itoa(b.ID)
		if dup {
			target += "&dup=1"
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
	})

	http.HandleFunc("/rename", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		id, _ := strconv.Atoi(r.FormValue("id"))
		store.rename(id, r.FormValue("title"))
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})

	http.HandleFunc("/delete", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		id, _ := strconv.Atoi(r.FormValue("id"))
		store.remove(id)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})

	log.Printf("PROTOTYPE bookmark server on http://localhost:%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
