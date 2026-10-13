package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// demoFmt is a 16-byte PCM fmt chunk: mono, 24 kHz, 16-bit (Kokoro's format).
func demoFmt() []byte {
	b := make([]byte, 16)
	binary.LittleEndian.PutUint16(b[0:], 1)
	binary.LittleEndian.PutUint16(b[2:], 1)
	binary.LittleEndian.PutUint32(b[4:], 24000)
	binary.LittleEndian.PutUint32(b[8:], 48000)
	binary.LittleEndian.PutUint16(b[12:], 2)
	binary.LittleEndian.PutUint16(b[14:], 16)
	return b
}

// fakeCaptionedTTS emulates a backend that returns word timings (like Kokoro's
// /dev/captioned_speech): one token per whitespace word, half a second each.
func fakeCaptionedTTS(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			writeJSON(w, http.StatusOK, map[string]any{"data": []any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/dev/captioned_speech":
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			text, _ := in["input"].(string)
			words := strings.Fields(text)
			ts := make([]map[string]any, 0, len(words))
			at := 0.0
			for _, word := range words {
				ts = append(ts, map[string]any{
					"word": word, "start_time": at, "end_time": at + 0.5,
				})
				at += 0.5
			}
			pcm := make([]byte, int(at*48000))
			wav := buildWAV(demoFmt(), pcm)
			writeJSON(w, http.StatusOK, map[string]any{
				"audio":        base64.StdEncoding.EncodeToString(wav),
				"audio_format": "audio/wav",
				"timestamps":   ts,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/audio/speech":
			http.NotFound(w, r) // force the captioned path
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func token(word string, start, end float64) rawToken {
	return rawToken{Word: word, Start: start, End: end}
}

// TestAlignTokens covers the text-normalization cases a speech backend applies:
// numbers and currency expand into several tokens while the reader still shows
// the original words.
func TestAlignTokens(t *testing.T) {
	chunk := "He paid $1,200 in 2024."
	tokens := []rawToken{
		token("He", 0.0, 0.2),
		token("paid", 0.2, 0.5),
		token("one", 0.5, 0.8),
		token("thousand", 0.8, 1.1),
		token(",", 1.1, 1.2),
		token("two", 1.2, 1.5),
		token("hundred", 1.5, 1.8),
		token("dollars", 1.8, 2.1),
		token("in", 2.1, 2.3),
		token("twenty", 2.3, 2.6),
		token("twenty-four", 2.6, 3.0),
		token(".", 3.0, 3.1),
	}
	got := alignTokens(chunk, tokens)
	want := []string{"He", "paid", "$1,200", "in", "2024."}
	if len(got) != len(want) {
		t.Fatalf("got %d timings, want %d: %+v", len(got), len(want), got)
	}
	prevEnd := -1.0
	for i, w := range want {
		if got[i].Word != w {
			t.Errorf("timing %d word = %q, want %q", i, got[i].Word, w)
		}
		if got[i].End <= got[i].Start {
			t.Errorf("timing %d (%q) not advancing: %+v", i, w, got[i])
		}
		if got[i].Start < prevEnd {
			t.Errorf("timing %d (%q) goes backwards: %+v", i, w, got[i])
		}
		prevEnd = got[i].End
	}
	// The expansion should cover all the tokens that made up "$1,200".
	if got[2].Start != 0.5 || got[2].End != 2.1 {
		t.Errorf("$1,200 span = [%v,%v], want [0.5,2.1]", got[2].Start, got[2].End)
	}
}

// TestAlignTokensUnspokenWord checks a displayed word with no matching token is
// left empty rather than stealing the next word's timing.
func TestAlignTokensUnspokenWord(t *testing.T) {
	got := alignTokens("Hello world", []rawToken{token("Hello", 0, 1)})
	if len(got) != 2 {
		t.Fatalf("got %d timings, want 2", len(got))
	}
	if got[0].Start != 0 || got[0].End != 1 {
		t.Errorf("Hello = %+v", got[0])
	}
	if got[1].Start != 1 || got[1].End != 1 {
		t.Errorf("world should be a zero-length marker at 1, got %+v", got[1])
	}
}

// TestChapterAudioTimings checks that a provider which returns timings gets them
// stored and served for the reader, with one timing per displayed word.
func TestChapterAudioTimings(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	ttsURL := fakeCaptionedTTS(t).URL
	cfg := Config{
		Addr: ":0", DataDir: dir, AdminUser: "admin", AdminPassword: "localread",
		TTSBaseURL: ttsURL, TTSModel: "kokoro", TTSVoice: "af_heart", TTSSpeed: 1, TTSLang: "a",
		MaxUploadBytes: 8 << 20,
	}
	if err := seedAdmin(store, cfg); err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateTTSProvider(TTSProvider{
		Name: "Kokoro", BaseURL: ttsURL, Model: "kokoro", Voice: "af_heart",
		Speed: 1, Lang: "a", Timings: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting(activeProviderKey, strconv.FormatInt(p.ID, 10)); err != nil {
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
		map[string]string{"title": "Timings", "text": "Chapter 1: Alpha\nAlpha beta gamma delta."})
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

	if code, body := doJSON(t, c, "POST", srv.URL+"/api/chapters/"+ch.ID+"/audio", nil); code != http.StatusOK {
		t.Fatalf("generate: %d %s", code, body)
	}
	code, body = doJSON(t, c, "GET", srv.URL+"/api/chapters/"+ch.ID, nil)
	if code != http.StatusOK {
		t.Fatalf("get chapter: %d %s", code, body)
	}
	var full Chapter
	if err := json.Unmarshal(body, &full); err != nil {
		t.Fatal(err)
	}
	code, body = doJSON(t, c, "GET", srv.URL+"/api/chapters/"+ch.ID+"/audio/timings", nil)
	if code != http.StatusOK {
		t.Fatalf("timings: %d %s", code, body)
	}
	var out struct {
		Timings []WordTiming `json:"timings"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	src := wordRe.FindAllString(full.Content, -1)
	if len(out.Timings) != len(src) {
		t.Fatalf("got %d timings, want one per word (%d): %+v", len(out.Timings), len(src), out.Timings)
	}
	for i, wt := range out.Timings {
		if wt.Word != src[i] {
			t.Errorf("timing %d word = %q, want %q", i, wt.Word, src[i])
		}
		if wt.End <= wt.Start {
			t.Errorf("timing %d (%q) not advancing: %+v", i, wt.Word, wt)
		}
	}
}
