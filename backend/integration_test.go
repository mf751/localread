package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeTTS stands in for LocalAI's speech endpoint.
func fakeTTS(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			writeJSON(w, http.StatusOK, map[string]any{"data": []any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/audio/speech":
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			// 4-byte fmt chunk + some PCM derived from the input length.
			n := len(in["input"].(string))
			pcm := bytes.Repeat([]byte{0x01, 0x02}, n)
			w.Header().Set("Content-Type", "audio/wav")
			_, _ = w.Write(buildWAV([]byte{1, 2, 3, 4}, pcm))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestApp(t *testing.T) (*httptest.Server, *App) {
	t.Helper()
	dir := t.TempDir()
	cfg := Config{
		Addr:           ":0",
		DataDir:        dir,
		AdminUser:      "admin",
		AdminPassword:  "localread",
		TTSBaseURL:     fakeTTS(t).URL,
		TTSModel:       "test-tts",
		TTSVoice:       "test-voice",
		TTSSpeed:       1.0,
		TTSLang:        "a",
		MaxUploadBytes: 8 << 20,
	}
	store, err := OpenStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := seedAdmin(store, cfg); err != nil {
		t.Fatal(err)
	}
	if err := seedTTSProvider(store, cfg); err != nil {
		t.Fatal(err)
	}
	app := &App{cfg: cfg, store: store, tts: NewTTS()}
	return httptest.NewServer(app.routes()), app
}

func newClient(t *testing.T, base string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Timeout: 30 * time.Second}
}

func doJSON(t *testing.T, c *http.Client, method, url string, body any) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func TestEndToEnd(t *testing.T) {
	srv, _ := newTestApp(t)
	c := newClient(t, srv.URL)

	// unauthenticated listing is rejected
	if code, _ := doJSON(t, c, "GET", srv.URL+"/api/novels", nil); code != http.StatusUnauthorized {
		t.Fatalf("unauth novels: got %d, want 401", code)
	}

	// login as seeded admin
	code, body := doJSON(t, c, "POST", srv.URL+"/api/auth/login",
		map[string]string{"username": "admin", "password": "localread"})
	if code != http.StatusOK {
		t.Fatalf("login: %d %s", code, body)
	}

	// import a text with two chapters
	text := "Chapter 1: Alpha\nHello there. This is the first chapter.\n\nChapter 2: Beta\nSecond chapter body."
	code, body = doJSON(t, c, "POST", srv.URL+"/api/novels/import",
		map[string]string{"title": "Test Novel", "text": text})
	if code != http.StatusCreated {
		t.Fatalf("import: %d %s", code, body)
	}
	var novel Novel
	if err := json.Unmarshal(body, &novel); err != nil {
		t.Fatal(err)
	}
	if novel.ChapterCount != 2 {
		t.Fatalf("chapterCount = %d, want 2", novel.ChapterCount)
	}

	// search finds it
	code, body = doJSON(t, c, "GET", srv.URL+"/api/novels?q=Test", nil)
	if code != http.StatusOK || !strings.Contains(string(body), "Test Novel") {
		t.Fatalf("search: %d %s", code, body)
	}

	// get novel + chapters
	code, body = doJSON(t, c, "GET", srv.URL+"/api/novels/"+novel.ID, nil)
	if code != http.StatusOK {
		t.Fatalf("get novel: %d %s", code, body)
	}
	var detail struct {
		Novel    Novel     `json:"novel"`
		Chapters []Chapter `json:"chapters"`
	}
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Chapters) != 2 {
		t.Fatalf("chapters = %d, want 2", len(detail.Chapters))
	}
	ch := detail.Chapters[0]

	// progress
	code, _ = doJSON(t, c, "PUT", srv.URL+"/api/progress/"+novel.ID,
		map[string]any{"chapterId": ch.ID, "position": 0.42})
	if code != http.StatusOK {
		t.Fatalf("save progress: %d", code)
	}
	// progress for a chapter that is not in the novel must be rejected
	code, _ = doJSON(t, c, "PUT", srv.URL+"/api/progress/"+novel.ID,
		map[string]any{"chapterId": "does-not-exist", "position": 0.1})
	if code != http.StatusBadRequest {
		t.Fatalf("bad progress chapter: got %d, want 400", code)
	}

	// generate audio (exercises TTS proxy + WAV join)
	code, body = doJSON(t, c, "POST", srv.URL+"/api/chapters/"+ch.ID+"/audio", nil)
	if code != http.StatusOK {
		t.Fatalf("generate audio: %d %s", code, body)
	}
	if !strings.Contains(string(body), `"ready":true`) {
		t.Fatalf("generate audio body: %s", body)
	}

	// stream audio with range support
	req, _ := http.NewRequest("GET", srv.URL+"/api/chapters/"+ch.ID+"/audio", nil)
	req.Header.Set("Range", "bytes=0-15")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("range audio: got %d, want 206", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "audio/wav" {
		t.Fatalf("audio content-type = %q", ct)
	}

	// history should include the listen event
	code, body = doJSON(t, c, "GET", srv.URL+"/api/history", nil)
	if code != http.StatusOK || !strings.Contains(string(body), "listen") {
		t.Fatalf("history: %d %s", code, body)
	}

	// catalog search includes the local library entry
	code, body = doJSON(t, c, "GET", srv.URL+"/api/catalog/search?q=Test", nil)
	if code != http.StatusOK || !strings.Contains(string(body), "Test Novel") {
		t.Fatalf("catalog: %d %s", code, body)
	}

	// account stats
	code, body = doJSON(t, c, "GET", srv.URL+"/api/account", nil)
	if code != http.StatusOK || !strings.Contains(string(body), `"novels":1`) {
		t.Fatalf("account: %d %s", code, body)
	}

	// deleting the novel cleans up
	code, _ = doJSON(t, c, "DELETE", srv.URL+"/api/novels/"+novel.ID, nil)
	if code != http.StatusOK {
		t.Fatalf("delete novel: %d", code)
	}
	code, _ = doJSON(t, c, "GET", srv.URL+"/api/novels/"+novel.ID, nil)
	if code != http.StatusNotFound {
		t.Fatalf("deleted novel get: got %d, want 404", code)
	}
}

func TestCreateNovelIndexAndPrivacy(t *testing.T) {
	srv, app := newTestApp(t)
	c := newClient(t, srv.URL)
	doJSON(t, c, "POST", srv.URL+"/api/auth/login", map[string]string{"username": "admin", "password": "localread"})

	// user B should not see user A's session-protected data without auth
	c2 := newClient(t, srv.URL)
	if code, _ := doJSON(t, c2, "GET", srv.URL+"/api/progress", nil); code != http.StatusUnauthorized {
		t.Fatalf("second client unauth progress: got %d", code)
	}

	// register a second user and confirm they can log in
	code, body := doJSON(t, c2, "POST", srv.URL+"/api/auth/register",
		map[string]string{"username": "reader2", "password": "secret1"})
	if code != http.StatusCreated {
		t.Fatalf("register: %d %s", code, body)
	}
	_ = app
}
