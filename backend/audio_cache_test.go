package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestAudioCacheIsPerVoiceModel checks that generated speech is cached per
// configured model: switching the active model regenerates instead of reusing
// another model's audio, each model keeps its own file, and switching back
// reuses the earlier cache. The frontend only ever talks to the backend.
func TestAudioCacheIsPerVoiceModel(t *testing.T) {
	dir := t.TempDir()
	ttsURL := fakeTTS(t).URL
	store, err := OpenStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	cfg := Config{
		Addr: ":0", DataDir: dir, AdminUser: "admin", AdminPassword: "localread",
		TTSBaseURL: ttsURL, TTSModel: "model-a", TTSVoice: "v", TTSSpeed: 1, TTSLang: "a",
		MaxUploadBytes: 8 << 20,
	}
	if err := seedAdmin(store, cfg); err != nil {
		t.Fatal(err)
	}

	pa, err := store.CreateTTSProvider(TTSProvider{Name: "A", BaseURL: ttsURL, Model: "model-a", Voice: "v", Speed: 1, Lang: "a", Timings: -1})
	if err != nil {
		t.Fatal(err)
	}
	pb, err := store.CreateTTSProvider(TTSProvider{Name: "B", BaseURL: ttsURL, Model: "model-b", Voice: "v", Speed: 1, Lang: "a", Timings: -1})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting(activeProviderKey, strconv.FormatInt(pa.ID, 10)); err != nil {
		t.Fatal(err)
	}

	app := &App{cfg: cfg, store: store, tts: NewTTS()}
	srv := httptest.NewServer(app.routes())
	t.Cleanup(srv.Close)

	c := newClient(t, srv.URL)
	if code, body := doJSON(t, c, "POST", srv.URL+"/api/auth/login",
		map[string]string{"username": "admin", "password": "localread"}); code != http.StatusOK {
		t.Fatalf("login: %d %s", code, body)
	}
	code, body := doJSON(t, c, "POST", srv.URL+"/api/novels/import",
		map[string]string{"title": "Cache Novel", "text": "Chapter 1: One\nBody text.\n\nChapter 2: Two\nMore."})
	if code != http.StatusCreated {
		t.Fatalf("import: %d %s", code, body)
	}
	var novel Novel
	if err := json.Unmarshal(body, &novel); err != nil {
		t.Fatal(err)
	}
	code, body = doJSON(t, c, "GET", srv.URL+"/api/novels/"+novel.ID, nil)
	if code != http.StatusOK {
		t.Fatalf("get novel: %d %s", code, body)
	}
	var detail struct {
		Chapters []Chapter `json:"chapters"`
	}
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatal(err)
	}
	ch := detail.Chapters[0]
	audio := "/api/chapters/" + ch.ID + "/audio"

	// Model A: first generate misses, the next one is served from cache.
	if code, body := doJSON(t, c, "POST", srv.URL+audio, nil); code != http.StatusOK || !strings.Contains(string(body), `"cached":false`) {
		t.Fatalf("generate A: %d %s", code, body)
	}
	if code, body := doJSON(t, c, "POST", srv.URL+audio, nil); code != http.StatusOK || !strings.Contains(string(body), `"cached":true`) {
		t.Fatalf("generate A again: %d %s", code, body)
	}

	// Switch to model B: it must not see A's cache.
	if code, body := doJSON(t, c, "POST", srv.URL+"/api/tts/providers/"+strconv.FormatInt(pb.ID, 10)+"/activate", nil); code != http.StatusOK {
		t.Fatalf("activate B: %d %s", code, body)
	}
	if code, body := doJSON(t, c, "GET", srv.URL+audio+"/status", nil); code != http.StatusOK || !strings.Contains(string(body), `"ready":false`) {
		t.Fatalf("status B before generate: %d %s", code, body)
	}
	if code, _ := doJSON(t, c, "GET", srv.URL+audio, nil); code != http.StatusNotFound {
		t.Fatalf("stream B before generate: got %d, want 404", code)
	}
	if code, body := doJSON(t, c, "POST", srv.URL+audio, nil); code != http.StatusOK || !strings.Contains(string(body), `"cached":false`) {
		t.Fatalf("generate B: %d %s", code, body)
	}

	// Each model has its own file named after its settings hash.
	hashA := NewTTS().settingsHash(pa)
	hashB := NewTTS().settingsHash(pb)
	if hashA == hashB {
		t.Fatal("model change did not change the settings hash")
	}
	for _, h := range []string{hashA, hashB} {
		p := filepath.Join(dir, "audio", novel.ID, ch.ID, h+".wav")
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("missing cached file for %s: %v", h, err)
		}
	}

	// Switching back to A is a cache hit again (its file survived B).
	if code, body := doJSON(t, c, "POST", srv.URL+"/api/tts/providers/"+strconv.FormatInt(pa.ID, 10)+"/activate", nil); code != http.StatusOK {
		t.Fatalf("activate A: %d %s", code, body)
	}
	if code, body := doJSON(t, c, "POST", srv.URL+audio, nil); code != http.StatusOK || !strings.Contains(string(body), `"cached":true`) {
		t.Fatalf("generate A after B: %d %s", code, body)
	}
}

// TestAudioCacheMigratesOldSchema makes sure an old single-column audio table is
// rebuilt rather than left in place (which would break per-model caching).
func TestAudioCacheMigratesOldSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.db")

	old, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.db.Exec(`
		DROP TABLE audio;
		CREATE TABLE audio (
		  chapter_id    TEXT PRIMARY KEY REFERENCES chapters(id) ON DELETE CASCADE,
		  path          TEXT NOT NULL,
		  settings_hash TEXT NOT NULL,
		  bytes         INTEGER NOT NULL DEFAULT 0,
		  created_at    TEXT NOT NULL
		);`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	// Reopening runs the migration.
	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("reopen after old schema: %v", err)
	}
	defer s.Close()

	var sqlText string
	if err := s.db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type='table' AND name='audio'`,
	).Scan(&sqlText); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sqlText, "PRIMARY KEY (chapter_id, settings_hash)") || !strings.Contains(sqlText, "timings") {
		t.Fatalf("audio table was not migrated: %s", sqlText)
	}
}
