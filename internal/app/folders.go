package app

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/afrieirham/bookmark-manager/internal/domain"
	"github.com/afrieirham/bookmark-manager/internal/importer"
)

type folderOption struct {
	ID    int64
	Title string
}

func folderLocation(id int64) string {
	if id == 0 {
		return "/"
	}
	return "/?folder=" + strconv.FormatInt(id, 10)
}

func folderID(raw string, fallback int64) (int64, bool) {
	if raw == "" {
		return fallback, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	return id, err == nil && id >= 0
}

func (s *Server) readFolder(r *http.Request, raw string, fallback int64) (int64, bool, error) {
	id, valid := folderID(raw, fallback)
	if !valid || id == 0 {
		return id, valid, nil
	}
	folders, err := s.store.ListFolders(r.Context())
	if err != nil {
		return 0, false, err
	}
	for _, f := range folders {
		if f.ID == id {
			return id, true, nil
		}
	}
	return 0, false, nil
}

func (s *Server) prepareFolders(w http.ResponseWriter, r *http.Request, data *startData) bool {
	folders, err := s.store.ListFolders(r.Context())
	if err != nil {
		s.fail(w, err)
		return false
	}
	byID := make(map[int64]domain.Folder, len(folders))
	for _, f := range folders {
		byID[f.ID] = f
	}
	fallback := int64(0)
	if data.Pending != nil {
		fallback = data.Pending.FolderID
	}
	id, valid := folderID(r.URL.Query().Get("folder"), fallback)
	current, exists := byID[id]
	if !valid || (id != 0 && !exists) {
		http.NotFound(w, r)
		return false
	}
	data.FolderID, data.Location = id, folderLocation(id)
	if id != 0 {
		data.CurrentFolder = &current
	}
	data.FolderOptions = []folderOption{{ID: 0, Title: "Start page"}}
	for _, f := range folders {
		if f.ParentID == id {
			data.Folders = append(data.Folders, f)
		}
		path := []string{f.Title}
		for parent := f.ParentID; parent != 0; {
			p, ok := byID[parent]
			if !ok || len(path) > len(folders) {
				break
			}
			path = append([]string{p.Title}, path...)
			parent = p.ParentID
		}
		data.FolderOptions = append(data.FolderOptions, folderOption{ID: f.ID, Title: strings.Join(path, " / ")})
	}
	for parent := id; parent != 0; {
		f, ok := byID[parent]
		if !ok || len(data.Breadcrumbs) > len(folders) {
			break
		}
		data.Breadcrumbs = append([]domain.Folder{f}, data.Breadcrumbs...)
		parent = f.ParentID
	}
	var visible []domain.Bookmark
	for _, b := range data.Bookmarks {
		if b.FolderID == id {
			visible = append(visible, b)
		}
	}
	data.Bookmarks = visible
	data.ImportOpen = r.URL.Query().Get("import") == "1"
	switch r.URL.Query().Get("import-error") {
	case "file":
		data.ImportError = "Choose a bookmarks HTML file exported from Chrome."
	case "large":
		data.ImportError = "That file is too large. Choose a bookmarks HTML file smaller than 20 MB."
	case "folder":
		data.ImportError = "The destination folder no longer exists. Choose another folder."
	}
	if data.ImportError != "" {
		data.ImportOpen = true
	}
	switch r.URL.Query().Get("folder-error") {
	case "title":
		data.FolderError = "Use a folder name of 1–160 characters that is unique within its parent."
	case "conflict":
		data.FolderError = "A subfolder has the same name as a folder in the parent. Rename it before removing this folder."
	case "missing":
		data.FolderError = "That folder no longer exists. Reload the page and try again."
	}
	if r.URL.Query().Get("imported") == "1" {
		counts := make([]int, 4)
		for i, key := range []string{"added", "folders", "duplicates", "skipped"} {
			n, err := strconv.Atoi(r.URL.Query().Get(key))
			if err != nil || n < 0 || n > 100000 {
				return true
			}
			counts[i] = n
		}
		data.Notice = fmt.Sprintf("Import complete: %d bookmarks added, %d folders created, %d duplicate URLs skipped, %d unsupported URLs skipped.", counts[0], counts[1], counts[2], counts[3])
	}
	return true
}

func (s *Server) folderError(w http.ResponseWriter, r *http.Request, id int64, code string) {
	loc := folderLocation(id)
	separator := "?"
	if id != 0 {
		separator = "&"
	}
	http.Redirect(w, r, loc+separator+"folder-error="+code, http.StatusSeeOther)
}

func (s *Server) handleCreateFolder(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	parent, valid, err := s.readFolder(r, r.PostForm.Get("parent"), 0)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !valid {
		s.folderError(w, r, 0, "missing")
		return
	}
	_, err = s.store.CreateFolder(r.Context(), r.PostForm.Get("title"), parent)
	if errors.Is(err, domain.ErrFolderTitle) {
		s.folderError(w, r, parent, "title")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, folderLocation(parent), http.StatusSeeOther)
}

func (s *Server) handleRenameFolder(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	id, valid, err := s.readFolder(r, r.PostForm.Get("id"), 0)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !valid || id == 0 {
		s.folderError(w, r, 0, "missing")
		return
	}
	if err := s.store.RenameFolder(r.Context(), id, r.PostForm.Get("title")); err != nil {
		if errors.Is(err, domain.ErrFolderTitle) {
			s.folderError(w, r, id, "title")
		} else {
			s.fail(w, err)
		}
		return
	}
	http.Redirect(w, r, folderLocation(id), http.StatusSeeOther)
}

func (s *Server) handleDeleteFolder(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	id, valid := folderID(r.PostForm.Get("id"), 0)
	if !valid || id == 0 {
		s.folderError(w, r, 0, "missing")
		return
	}
	folders, err := s.store.ListFolders(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	for _, f := range folders {
		if f.ID != id {
			continue
		}
		if err := s.store.DeleteFolder(r.Context(), id); err != nil {
			switch {
			case errors.Is(err, domain.ErrFolderTitle):
				s.folderError(w, r, id, "conflict")
			case errors.Is(err, domain.ErrFolder):
				s.folderError(w, r, 0, "missing")
			default:
				s.fail(w, err)
			}
			return
		}
		http.Redirect(w, r, folderLocation(f.ParentID), http.StatusSeeOther)
		return
	}
	s.folderError(w, r, 0, "missing")
}

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	const maxImportSize = 20 << 20
	r.Body = http.MaxBytesReader(w, r.Body, maxImportSize)
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var sizeError *http.MaxBytesError
		if errors.As(err, &sizeError) {
			s.importError(w, r, "large", http.StatusRequestEntityTooLarge)
		} else {
			s.importError(w, r, "file", http.StatusBadRequest)
		}
		return
	}
	parent, valid, err := s.readFolder(r, r.PostForm.Get("folder"), 0)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !valid {
		s.importError(w, r, "folder", http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		s.importError(w, r, "file", http.StatusBadRequest)
		return
	}
	defer file.Close()
	doc, err := importer.Parse(file)
	if err != nil {
		s.importError(w, r, "file", http.StatusBadRequest)
		return
	}
	result, err := s.store.Import(r.Context(), doc, parent, s.now())
	if errors.Is(err, domain.ErrFolder) {
		s.importError(w, r, "folder", http.StatusBadRequest)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	loc := fmt.Sprintf("/?folder=%d&imported=1&added=%d&folders=%d&duplicates=%d&skipped=%d", parent, result.Added, result.Folders, result.Duplicates, result.Skipped)
	http.Redirect(w, r, loc, http.StatusSeeOther)
}

func (s *Server) importError(w http.ResponseWriter, r *http.Request, code string, status int) {
	copy := r.Clone(r.Context())
	urlCopy := *r.URL
	copy.URL = &urlCopy
	query := copy.URL.Query()
	query.Set("import-error", code)
	if id, valid, err := s.readFolder(r, r.PostForm.Get("folder"), 0); err == nil && valid {
		query.Set("folder", strconv.FormatInt(id, 10))
	} else {
		query.Del("folder")
	}
	copy.URL.RawQuery = query.Encode()
	s.renderStart(w, copy, status)
}
