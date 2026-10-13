package main

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (a *App) handleListNovels(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	novels, err := a.store.ListNovels(q)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not list novels")
		return
	}
	if novels == nil {
		novels = []Novel{}
	}
	writeJSON(w, http.StatusOK, novels)
}

func (a *App) handleGetNovel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	n, err := a.store.GetNovel(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "novel not found")
		return
	}
	chapters, err := a.store.ListChapters(id, false)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load chapters")
		return
	}
	if chapters == nil {
		chapters = []Chapter{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"novel": n, "chapters": chapters})
}

func (a *App) handleDeleteNovel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.store.DeleteNovel(id); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not delete novel")
		return
	}
	// Remove cached audio for the novel's chapters, staying inside the data root.
	if dir, err := safeJoin(a.cfg.DataDir, filepath.Join("audio", id)); err == nil {
		// #nosec G304 -- dir is validated by safeJoin against the data root.
		_ = os.RemoveAll(dir)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) handleGetChapter(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, err := a.store.GetChapter(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "chapter not found")
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// handleImport accepts either a multipart upload (field "file") or a JSON body
// with raw text, and creates a novel with parsed chapters.
func (a *App) handleImport(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())

	ct := r.Header.Get("Content-Type")
	var (
		title, author string
		chapters      []ChapterInput
	)

	switch {
	case strings.HasPrefix(ct, "multipart/form-data"):
		if err := r.ParseMultipartForm(a.cfg.MaxUploadBytes); err != nil {
			writeErr(w, http.StatusBadRequest, "could not read upload")
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeErr(w, http.StatusBadRequest, "missing file field")
			return
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, a.cfg.MaxUploadBytes))
		if err != nil {
			writeErr(w, http.StatusBadRequest, "could not read file")
			return
		}
		t, au, chs, err := parseNovelFile(header.Filename, data)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		title, author, chapters = t, au, chs
		// Explicit form overrides win over parsed metadata.
		if v := strings.TrimSpace(r.FormValue("title")); v != "" {
			title = v
		}
		if v := strings.TrimSpace(r.FormValue("author")); v != "" {
			author = v
		}
	case strings.HasPrefix(ct, "application/json"):
		var in struct {
			Title    string `json:"title"`
			Author   string `json:"author"`
			Text     string `json:"text"`
			Chapters []struct {
				Title   string `json:"title"`
				Content string `json:"content"`
			} `json:"chapters"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}
		title, author = in.Title, in.Author
		if len(in.Chapters) > 0 {
			for _, c := range in.Chapters {
				chapters = append(chapters, ChapterInput{Title: c.Title, Content: c.Content})
			}
		} else {
			chapters = splitTextChapters(in.Text)
		}
	default:
		writeErr(w, http.StatusUnsupportedMediaType, "use multipart/form-data or application/json")
		return
	}

	chapters = dropEmpty(chapters)
	if len(chapters) == 0 {
		writeErr(w, http.StatusBadRequest, "no readable chapters found in upload")
		return
	}
	if title == "" {
		title = "Untitled"
	}

	n, err := a.store.CreateNovel(NovelInput{
		Title:       title,
		Author:      author,
		Description: firstParagraph(chapters),
		Source:      "upload",
		CreatedBy:   u.ID,
		Chapters:    chapters,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save novel")
		return
	}
	_ = a.store.AddHistory(u.ID, n.ID, "", "import", title)
	writeJSON(w, http.StatusCreated, n)
}

func dropEmpty(chs []ChapterInput) []ChapterInput {
	out := chs[:0]
	for _, c := range chs {
		if strings.TrimSpace(c.Content) == "" {
			continue
		}
		out = append(out, c)
	}
	return out
}

func firstParagraph(chs []ChapterInput) string {
	for _, c := range chs {
		t := strings.TrimSpace(c.Content)
		if t == "" {
			continue
		}
		if i := strings.Index(t, "\n\n"); i > 0 && i < 400 {
			return t[:i]
		}
		if len(t) > 400 {
			return t[:400] + "…"
		}
		return t
	}
	return ""
}

var _ = errors.New
