// Package importer reads the Netscape bookmark HTML exported by Chrome.
package importer

import (
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/afrieirham/bookmark-manager/internal/domain"
)

var ErrInvalid = errors.New("choose a bookmarks HTML file exported from Chrome")

// Parse reads tokens rather than an HTML5 DOM: browser exports omit DT and P
// closing tags, and HTML5 tree repair can change their folder hierarchy.
// It validates the whole document before anything reaches storage.
func Parse(r io.Reader) (domain.ImportDocument, error) {
	var doc domain.ImportDocument
	z := html.NewTokenizer(r)
	z.SetMaxBuf(2 << 20)
	var stack []int64
	var capture, href, added, pending string
	var text strings.Builder
	marker, sawList := false, false
	entries := 0
	for {
		switch z.Next() {
		case html.ErrorToken:
			if z.Err() != io.EOF || !marker || !sawList || len(stack) != 0 || capture != "" || pending != "" {
				return domain.ImportDocument{}, ErrInvalid
			}
			return doc, nil
		case html.DoctypeToken:
			marker = strings.EqualFold(strings.TrimSpace(z.Token().Data), "NETSCAPE-Bookmark-file-1")
		case html.StartTagToken:
			t := z.Token()
			switch t.Data {
			case "dl":
				if !marker || capture != "" || len(stack) >= 128 {
					return domain.ImportDocument{}, ErrInvalid
				}
				parent := int64(0)
				if len(stack) > 0 {
					parent = stack[len(stack)-1]
				}
				if pending != "" {
					id := int64(len(doc.Folders) + 1)
					doc.Folders = append(doc.Folders, domain.Folder{ID: id, Title: pending, ParentID: parent})
					parent, pending = id, ""
				}
				stack = append(stack, parent)
				sawList = true
			case "h3", "a":
				if len(stack) == 0 || capture != "" || pending != "" {
					return domain.ImportDocument{}, ErrInvalid
				}
				entries++
				if entries > 100000 {
					return domain.ImportDocument{}, errors.New("bookmark file has too many entries (maximum 100,000)")
				}
				capture, href, added = t.Data, "", ""
				text.Reset()
				for _, attr := range t.Attr {
					switch attr.Key {
					case "href":
						href = attr.Val
					case "add_date":
						added = attr.Val
					}
				}
			}
		case html.TextToken:
			if capture != "" {
				text.Write(z.Text())
			}
		case html.EndTagToken:
			t := z.Token()
			if t.Data == "dl" {
				if len(stack) == 0 || capture != "" || pending != "" {
					return domain.ImportDocument{}, ErrInvalid
				}
				stack = stack[:len(stack)-1]
			}
			if capture == "" || t.Data != capture {
				continue
			}
			title := strings.TrimSpace(text.String())
			if capture == "h3" {
				if title == "" {
					title = "Untitled folder"
				}
				pending = title
			} else {
				// Exports contain absolute URLs. Never turn browser-internal URLs,
				// file URLs, or bookmarklets into fetchable web addresses.
				u, err := url.Parse(strings.TrimSpace(href))
				normalized, ok := domain.NormalizeURL(href)
				if err != nil || (u.Scheme != "https" && u.Scheme != "http") || !ok {
					doc.Skipped++
				} else {
					b := domain.Bookmark{URL: normalized, Title: domain.FallbackTitle(normalized, title, "", false), FolderID: stack[len(stack)-1]}
					if seconds, err := strconv.ParseInt(added, 10, 64); err == nil && seconds > 0 && seconds <= 253402300799 {
						b.CreatedAt = time.Unix(seconds, 0).UTC()
					}
					doc.Bookmarks = append(doc.Bookmarks, b)
				}
			}
			capture = ""
		}
	}
}
