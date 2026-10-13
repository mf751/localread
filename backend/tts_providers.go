package main

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const activeProviderKey = "tts_active_provider"

// seedTTSProvider creates a provider from the environment config on an empty
// database and makes it active, preserving the previous out-of-the-box setup.
func seedTTSProvider(s *Store, cfg Config) error {
	n, err := s.CountTTSProviders()
	if err != nil || n > 0 {
		return err
	}
	name := cfg.TTSModel
	if name == "" {
		name = "Default"
	}
	p, err := s.CreateTTSProvider(TTSProvider{
		Name:    name,
		BaseURL: cfg.TTSBaseURL,
		Model:   cfg.TTSModel,
		Voice:   cfg.TTSVoice,
		Speed:   cfg.TTSSpeed,
		Lang:    cfg.TTSLang,
		Timings: -1,
	})
	if err != nil {
		return err
	}
	return s.SetSetting(activeProviderKey, strconv.FormatInt(p.ID, 10))
}

func (a *App) activeProviderID() int64 {
	if v, err := a.store.GetSetting(activeProviderKey); err == nil {
		if n, e := strconv.ParseInt(v, 10, 64); e == nil {
			return n
		}
	}
	return 0
}

// activeProvider resolves the selected provider, falling back to the first one.
func (a *App) activeProvider() (TTSProvider, error) {
	if id := a.activeProviderID(); id != 0 {
		if p, err := a.store.GetTTSProvider(id); err == nil {
			return p, nil
		}
	}
	list, err := a.store.ListTTSProviders()
	if err != nil {
		return TTSProvider{}, err
	}
	if len(list) == 0 {
		return TTSProvider{}, errNotFound
	}
	return list[0], nil
}

// ttsStatus reports the active provider's status for health/account views.
func (a *App) ttsStatus() TTSStatus {
	p, err := a.activeProvider()
	if err != nil {
		return TTSStatus{}
	}
	return a.tts.status(p)
}

type ttsProviderView struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	BaseURL   string  `json:"baseUrl"`
	Model     string  `json:"model"`
	Voice     string  `json:"voice"`
	Speed     float64 `json:"speed"`
	Lang      string  `json:"lang"`
	HasKey    bool    `json:"hasKey"`
	Timings   string  `json:"timings"` // "yes" | "no" | "unknown"
	Active    bool    `json:"active"`
	CreatedAt string  `json:"createdAt"`
}

func providerView(p TTSProvider, active bool) ttsProviderView {
	timing := "unknown"
	switch p.Timings {
	case 0:
		timing = "no"
	case 1:
		timing = "yes"
	}
	return ttsProviderView{
		ID: p.ID, Name: p.Name, BaseURL: p.BaseURL, Model: p.Model, Voice: p.Voice,
		Speed: p.Speed, Lang: p.Lang, HasKey: p.APIKey != "", Timings: timing,
		Active: active, CreatedAt: p.CreatedAt,
	}
}

func (a *App) listProviderViews() ([]ttsProviderView, error) {
	list, err := a.store.ListTTSProviders()
	if err != nil {
		return nil, err
	}
	activeID := a.activeProviderID()
	views := make([]ttsProviderView, 0, len(list))
	for _, p := range list {
		views = append(views, providerView(p, p.ID == activeID))
	}
	return views, nil
}

func (a *App) handleListTTSProviders(w http.ResponseWriter, r *http.Request) {
	views, err := a.listProviderViews()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not list models")
		return
	}
	if views == nil {
		views = []ttsProviderView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": views, "activeId": a.activeProviderID()})
}

type ttsProviderInput struct {
	Name    string  `json:"name"`
	BaseURL string  `json:"baseUrl"`
	APIKey  string  `json:"apiKey"`
	Model   string  `json:"model"`
	Voice   string  `json:"voice"`
	Speed   float64 `json:"speed"`
	Lang    string  `json:"lang"`
}

func normalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("URL must be a valid http(s) URL")
	}
	// Be forgiving when a full endpoint is pasted instead of the server root,
	// e.g. "http://localhost:8080/v1/audio/speech" -> "http://localhost:8080".
	p := strings.TrimRight(u.Path, "/")
	for _, suffix := range []string{"/dev/captioned_speech", "/v1/audio/speech", "/v1/audio", "/v1"} {
		if strings.HasSuffix(p, suffix) {
			p = strings.TrimSuffix(p, suffix)
			break
		}
	}
	u.Path = strings.TrimRight(p, "/")
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/"), nil
}

// probeResult is the JSON shape shared by create, update and test.
func (a *App) probeResult(p TTSProvider, timings bool, err error) map[string]any {
	detail := ""
	if err != nil {
		detail = err.Error()
	}
	return map[string]any{
		"provider":  providerView(p, p.ID == a.activeProviderID()),
		"reachable": err == nil,
		"timings":   timings,
		"detail":    detail,
	}
}

func (a *App) handleCreateTTSProvider(w http.ResponseWriter, r *http.Request) {
	var in ttsProviderInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	base, err := normalizeBaseURL(in.BaseURL)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.Speed <= 0 {
		in.Speed = 1.0
	}
	p, err := a.store.CreateTTSProvider(TTSProvider{
		Name: in.Name, BaseURL: base, APIKey: in.APIKey,
		Model: in.Model, Voice: in.Voice, Speed: in.Speed, Lang: in.Lang,
		Timings: -1,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not create model")
		return
	}
	// Best-effort probe so the list can show whether it returns timings.
	timings, perr := a.tts.probe(p)
	if perr == nil {
		if timings {
			p.Timings = 1
		} else {
			p.Timings = 0
		}
		_ = a.store.UpdateTTSProvider(p)
	}
	// Make the first provider active automatically.
	if a.activeProviderID() == 0 {
		_ = a.store.SetSetting(activeProviderKey, strconv.FormatInt(p.ID, 10))
	}
	writeJSON(w, http.StatusCreated, a.probeResult(p, timings, perr))
}

func (a *App) handleUpdateTTSProvider(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	p, err := a.store.GetTTSProvider(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "model not found")
		return
	}
	var in ttsProviderInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) != "" {
		p.Name = strings.TrimSpace(in.Name)
	}
	if strings.TrimSpace(in.BaseURL) != "" {
		base, uerr := normalizeBaseURL(in.BaseURL)
		if uerr != nil {
			writeErr(w, http.StatusBadRequest, uerr.Error())
			return
		}
		p.BaseURL = base
	}
	// An empty key means "leave the stored key unchanged".
	if in.APIKey != "" {
		p.APIKey = in.APIKey
	}
	p.Model = in.Model
	p.Voice = in.Voice
	if in.Speed > 0 {
		p.Speed = in.Speed
	}
	p.Lang = in.Lang
	// Configuration changed: forget the previously detected timing capability.
	p.Timings = -1
	timings, perr := a.tts.probe(p)
	if perr == nil {
		if timings {
			p.Timings = 1
		} else {
			p.Timings = 0
		}
	}
	if err := a.store.UpdateTTSProvider(p); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not update model")
		return
	}
	writeJSON(w, http.StatusOK, a.probeResult(p, timings, perr))
}

func (a *App) handleDeleteTTSProvider(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := a.store.DeleteTTSProvider(id); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not delete model")
		return
	}
	if a.activeProviderID() == id {
		_ = a.store.SetSetting(activeProviderKey, "")
		// Promote the first remaining provider, if any.
		if list, err := a.store.ListTTSProviders(); err == nil && len(list) > 0 {
			_ = a.store.SetSetting(activeProviderKey, strconv.FormatInt(list[0].ID, 10))
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) handleActivateTTSProvider(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if _, err := a.store.GetTTSProvider(id); err != nil {
		writeErr(w, http.StatusNotFound, "model not found")
		return
	}
	if err := a.store.SetSetting(activeProviderKey, strconv.FormatInt(id, 10)); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not set active model")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) handleTestTTSProvider(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	p, err := a.store.GetTTSProvider(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "model not found")
		return
	}
	timings, perr := a.tts.probe(p)
	reachable := perr == nil
	if reachable {
		if timings {
			p.Timings = 1
		} else {
			p.Timings = 0
		}
		_ = a.store.UpdateTTSProvider(p)
	}
	detail := ""
	if perr != nil {
		detail = perr.Error()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"reachable": reachable,
		"timings":   timings,
		"detail":    detail,
		"provider":  providerView(p, p.ID == a.activeProviderID()),
		"tts":       a.tts.status(p),
	})
}
