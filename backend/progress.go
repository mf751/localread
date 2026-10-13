package main

import (
	"net/http"
	"strconv"
)

func (a *App) handleListProgress(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	list, err := a.store.ListProgress(u.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load progress")
		return
	}
	if list == nil {
		list = []Progress{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *App) handleGetProgress(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	p, err := a.store.GetProgress(u.ID, r.PathValue("novelID"))
	if err != nil {
		writeJSON(w, http.StatusOK, nil) // no progress yet
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *App) handleSaveProgress(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	novelID := r.PathValue("novelID")

	var in struct {
		ChapterID string  `json:"chapterId"`
		Position  float64 `json:"position"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Position < 0 {
		in.Position = 0
	}
	if in.Position > 1 {
		in.Position = 1
	}
	if _, err := a.store.GetNovel(novelID); err != nil {
		writeErr(w, http.StatusNotFound, "novel not found")
		return
	}
	ch, err := a.store.GetChapter(in.ChapterID)
	if err != nil || ch.NovelID != novelID {
		writeErr(w, http.StatusBadRequest, "chapter does not belong to novel")
		return
	}
	if err := a.store.SaveProgress(u.ID, novelID, in.ChapterID, in.Position); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save progress")
		return
	}
	p, _ := a.store.GetProgress(u.ID, novelID)
	writeJSON(w, http.StatusOK, p)
}

func (a *App) handleListHistory(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := a.store.ListHistory(u.ID, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load history")
		return
	}
	if list == nil {
		list = []HistoryEvent{}
	}
	writeJSON(w, http.StatusOK, list)
}

// handleAddHistory records an explicit event (opened/wrote/listened/imported).
func (a *App) handleAddHistory(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	var in struct {
		NovelID   string `json:"novelId"`
		ChapterID string `json:"chapterId"`
		Event     string `json:"event"`
		Detail    string `json:"detail"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Event == "" {
		in.Event = "open"
	}
	allowed := map[string]bool{"open": true, "listen": true, "finish": true, "import": true, "note": true}
	if !allowed[in.Event] {
		writeErr(w, http.StatusBadRequest, "unknown event type")
		return
	}
	if _, err := a.store.GetNovel(in.NovelID); err != nil {
		writeErr(w, http.StatusNotFound, "novel not found")
		return
	}
	if in.ChapterID != "" {
		if ch, err := a.store.GetChapter(in.ChapterID); err != nil || ch.NovelID != in.NovelID {
			writeErr(w, http.StatusBadRequest, "chapter does not belong to novel")
			return
		}
	}
	if err := a.store.AddHistory(u.ID, in.NovelID, in.ChapterID, in.Event, in.Detail); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save history")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]bool{"ok": true})
}
