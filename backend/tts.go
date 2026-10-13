package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// TTS proxies chapter text to a configured speech backend and caches the
// resulting WAV (plus optional word timings) under the data root.
type TTS struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex // per-chapter generation lock
}

func NewTTS() *TTS {
	return &TTS{locks: map[string]*sync.Mutex{}}
}

type TTSStatus struct {
	Provider  string  `json:"provider"`
	BaseURL   string  `json:"baseUrl"`
	Model     string  `json:"model"`
	Voice     string  `json:"voice"`
	Speed     float64 `json:"speed"`
	Timings   bool    `json:"timings"`
	Reachable bool    `json:"reachable"`
}

// WordTiming is one word's playback window in absolute chapter-audio seconds.
// The frontend renders words in the same order and zips them by index.
type WordTiming struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Word  string  `json:"word"`
}

// rawToken is one token as returned by a captioned speech endpoint.
type rawToken struct {
	Word       string
	Start, End float64
}

func (t *TTS) settingsHash(p TTSProvider) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%d|%s|%s|%s|%.3f|%s|v2",
		p.ID, p.BaseURL, p.Model, p.Voice, p.Speed, p.Lang)))
	return hex.EncodeToString(h[:8])
}

func (t *TTS) lock(chapterID string) *sync.Mutex {
	t.mu.Lock()
	defer t.mu.Unlock()
	m, ok := t.locks[chapterID]
	if !ok {
		m = &sync.Mutex{}
		t.locks[chapterID] = m
	}
	return m
}

// audioPath returns the data-root-relative cache path for a chapter's audio
// under one voice-model configuration. The settings hash is part of the path,
// so a different model/voice writes a new file instead of overwriting (and
// losing) the previous model's cached audio.
func (t *TTS) audioPath(novelID, chapterID, settingsHash string) string {
	return filepath.Join("audio", novelID, chapterID, settingsHash+".wav")
}

func (t *TTS) status(p TTSProvider) TTSStatus {
	st := TTSStatus{
		Provider: p.Name,
		BaseURL:  p.BaseURL,
		Model:    p.Model,
		Voice:    p.Voice,
		Speed:    p.Speed,
		Timings:  p.Timings == 1,
	}
	if p.BaseURL == "" {
		return st
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.BaseURL, "/")+"/v1/models", nil)
	if err != nil {
		return st
	}
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return st
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	st.Reachable = resp.StatusCode < 500
	return st
}

// synthesize renders one chunk of text. If the provider can return word
// timings it uses them; otherwise it just returns the audio with nil timings.
func (t *TTS) synthesize(p TTSProvider, text string) (wav []byte, timings []WordTiming, err error) {
	return t.synthesizeCtx(context.Background(), p, text)
}

func (t *TTS) synthesizeCtx(ctx context.Context, p TTSProvider, text string) (wav []byte, timings []WordTiming, err error) {
	if p.Timings == 0 {
		w, e := t.synthPlain(ctx, p, text)
		return w, nil, e
	}
	w, toks, cerr := t.synthCaptioned(ctx, p, text)
	if cerr == nil {
		if len(toks) == 0 {
			// Audio (or JSON) but no word timings: treat as an audio-only model.
			return w, nil, nil
		}
		return w, alignTokens(text, toks), nil
	}
	// Captioned endpoint unavailable: fall back to plain audio.
	w2, perr := t.synthPlain(ctx, p, text)
	if perr != nil {
		return nil, nil, cerr
	}
	return w2, nil, nil
}

func (t *TTS) basePayload(p TTSProvider, text string) map[string]any {
	payload := map[string]any{
		"model":           p.Model,
		"input":           text,
		"voice":           p.Voice,
		"response_format": "wav",
	}
	if p.Speed > 0 {
		payload["speed"] = p.Speed
	}
	if p.Lang != "" {
		payload["lang_code"] = p.Lang
	}
	return payload
}

func (t *TTS) post(ctx context.Context, p TTSProvider, path string, payload map[string]any) ([]byte, error) {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach tts service at %s: %w", p.BaseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("tts returned %d: %s", resp.StatusCode, bytes.TrimSpace(msg))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 96<<20))
}

// synthCaptioned speaks via the captioned endpoint, which returns JSON with
// base64 WAV audio and per-word timestamps (e.g. Kokoro).
func (t *TTS) synthCaptioned(ctx context.Context, p TTSProvider, text string) ([]byte, []rawToken, error) {
	payload := t.basePayload(p, text)
	payload["stream"] = false
	payload["return_timestamps"] = true
	raw, err := t.post(ctx, p, "/dev/captioned_speech", payload)
	if err != nil {
		return nil, nil, err
	}
	var cr struct {
		Audio      string `json:"audio"`
		Timestamps []struct {
			Word      string  `json:"word"`
			StartTime float64 `json:"start_time"`
			EndTime   float64 `json:"end_time"`
		} `json:"timestamps"`
	}
	if err := json.Unmarshal(raw, &cr); err != nil {
		return nil, nil, fmt.Errorf("captioned endpoint did not return JSON: %w", err)
	}
	if cr.Audio == "" {
		return nil, nil, errors.New("captioned response had no audio")
	}
	wav, err := base64.StdEncoding.DecodeString(cr.Audio)
	if err != nil {
		return nil, nil, fmt.Errorf("captioned audio is not valid base64: %w", err)
	}
	toks := make([]rawToken, 0, len(cr.Timestamps))
	for _, ts := range cr.Timestamps {
		toks = append(toks, rawToken{Word: ts.Word, Start: ts.StartTime, End: ts.EndTime})
	}
	return wav, toks, nil
}

// synthPlain speaks via the OpenAI-compatible /v1/audio/speech endpoint and
// returns raw WAV bytes. Some servers answer with JSON ({audio: <base64>}); both
// shapes are handled.
func (t *TTS) synthPlain(ctx context.Context, p TTSProvider, text string) ([]byte, error) {
	data, err := t.post(ctx, p, "/v1/audio/speech", t.basePayload(p, text))
	if err != nil {
		return nil, err
	}
	if len(data) > 0 && data[0] != 'R' { // not RIFF
		var jr struct {
			Audio string `json:"audio"`
		}
		if json.Unmarshal(data, &jr) == nil && jr.Audio != "" {
			if wav, derr := base64.StdEncoding.DecodeString(jr.Audio); derr == nil {
				return wav, nil
			}
		}
	}
	return data, nil
}

// probe checks reachability and whether the provider returns word timings. The
// returned error carries the model's own message so the UI can explain a
// failure (wrong URL, bad voice, model not loaded, …).
func (t *TTS) probe(p TTSProvider) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var captErr error
	if wav, toks, cerr := t.synthCaptioned(ctx, p, "Hello world."); cerr == nil && len(wav) > 0 {
		return len(toks) > 0, nil
	} else if cerr != nil {
		captErr = cerr
	}
	if wav, perr := t.synthPlain(ctx, p, "Hello world."); perr == nil && len(wav) > 0 {
		return false, nil
	} else if perr != nil {
		return false, perr
	}
	if captErr != nil {
		return false, captErr
	}
	return false, errors.New("provider returned no audio")
}

// ---------- word alignment ----------

var nonAlnum = regexp.MustCompile(`[^\p{L}\p{N}]+`)

func normWord(s string) string {
	return strings.ToLower(nonAlnum.ReplaceAllString(s, ""))
}

// alignTokens maps a captioned endpoint's spoken tokens back to the source
// words that are displayed. Speech backends normalize text (numbers become
// words, symbols expand), so a source word can span several tokens; this walks
// both in order, grouping expansions and leaving unmatched words with no time.
func alignTokens(chunk string, tokens []rawToken) []WordTiming {
	words := wordRe.FindAllString(chunk, -1)
	out := make([]WordTiming, len(words))
	ti := 0
	lastEnd := 0.0
	for si, w := range words {
		nw := normWord(w)
		// Words the backend did not speak (punctuation-only, or dropped) get a
		// zero-length marker at the current position, keeping starts ordered.
		out[si] = WordTiming{Start: lastEnd, End: lastEnd, Word: w}
		if nw == "" || ti >= len(tokens) {
			continue
		}
		start := tokens[ti].Start
		startSet := false
		matched := false
		j := ti
		for j < len(tokens) {
			tk := tokens[j]
			if normWord(tk.Word) == nw {
				if !startSet {
					start = tk.Start
					startSet = true
				}
				out[si] = WordTiming{Start: start, End: tk.End, Word: w}
				j++
				matched = true
				break
			}
			// Stop if this token looks like a later source word: then w was not
			// spoken and must not swallow the following words' tokens.
			if matchesLaterWord(words, si+1, tk.Word, 4) {
				break
			}
			if !startSet {
				start = tk.Start
				startSet = true
			}
			j++
		}
		if !matched && startSet && j > ti {
			// Consumed as an expansion with no exact token match; use the span.
			out[si] = WordTiming{Start: start, End: tokens[j-1].End, Word: w}
		}
		if out[si].End > out[si].Start {
			lastEnd = out[si].End
		}
		ti = j
	}
	return out
}

// matchesLaterWord reports whether token matches any of the next k source words
// starting at index from.
func matchesLaterWord(words []string, from int, token string, k int) bool {
	nt := normWord(token)
	if nt == "" {
		return false
	}
	for x := from; x < len(words) && x < from+k; x++ {
		if normWord(words[x]) == nt {
			return true
		}
	}
	return false
}

// ---------- generation ----------

// generateChapterAudio synthesizes a chapter and writes the cached WAV plus any
// word timings, returning the audio size.
func (a *App) generateChapterAudio(novel Novel, ch Chapter, p TTSProvider) (int64, error) {
	if !safeID(ch.ID) || !safeID(novel.ID) {
		return 0, ErrUnsafePath
	}

	hash := a.tts.settingsHash(p)
	rel := a.tts.audioPath(novel.ID, ch.ID, hash)
	abs, err := safeJoin(a.cfg.DataDir, rel)
	if err != nil {
		return 0, err
	}
	if err := ensureDir(filepath.Dir(abs)); err != nil {
		return 0, err
	}

	chunks := sentenceSplit(ch.Content, 900)
	if len(chunks) == 0 {
		return 0, errors.New("chapter has no text to read")
	}

	var parts [][]byte
	var allTimings []WordTiming
	offset := 0.0
	sawTimings := false
	for i, c := range chunks {
		wav, timings, err := a.tts.synthesize(p, c)
		if err != nil {
			return 0, fmt.Errorf("synthesis failed at chunk %d/%d: %w", i+1, len(chunks), err)
		}
		parts = append(parts, wav)
		if wd, werr := parseWAV(wav); werr == nil {
			if timings != nil {
				sawTimings = true
				for _, wt := range timings {
					allTimings = append(allTimings, WordTiming{
						Start: wt.Start + offset,
						End:   wt.End + offset,
						Word:  wt.Word,
					})
				}
			}
			offset += wavDuration(wd)
		}
	}

	joined, err := joinWAV(parts)
	if err != nil {
		return 0, err
	}

	tmp := abs + ".tmp"
	// #nosec G304 -- abs is produced by safeJoin, which rejects traversal.
	if err := os.WriteFile(tmp, joined, 0o600); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, abs); err != nil {
		return 0, err
	}

	timingsJSON := ""
	if len(allTimings) > 0 {
		if b, merr := json.Marshal(allTimings); merr == nil {
			timingsJSON = string(b)
		}
	}
	if err := a.store.PutAudio(ch.ID, rel, hash, int64(len(joined)), timingsJSON); err != nil {
		return 0, err
	}

	// Remember what we learned about this provider so later runs skip the probe.
	if p.Timings == -1 {
		p.Timings = 0
		if sawTimings {
			p.Timings = 1
		}
		_ = a.store.UpdateTTSProvider(p)
	}
	return int64(len(joined)), nil
}

// ---------- HTTP handlers ----------

func (a *App) handleChapterAudioStatus(w http.ResponseWriter, r *http.Request) {
	ch, err := a.store.GetChapter(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "chapter not found")
		return
	}
	p, perr := a.activeProvider()
	if perr != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ready": false, "tts": TTSStatus{}})
		return
	}
	rec, err := a.store.GetAudio(ch.ID, a.tts.settingsHash(p))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ready": false, "tts": a.tts.status(p)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ready":   true,
		"bytes":   rec.Bytes,
		"timings": rec.Timings != "",
		"tts":     a.tts.status(p),
	})
}

func (a *App) handleGenerateAudio(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	ch, err := a.store.GetChapter(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "chapter not found")
		return
	}
	novel, err := a.store.GetNovel(ch.NovelID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "novel not found")
		return
	}
	p, err := a.activeProvider()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "no TTS model configured")
		return
	}

	// Serialize generation for a given chapter.
	lock := a.tts.lock(ch.ID)
	lock.Lock()
	defer lock.Unlock()

	if rec, err := a.store.GetAudio(ch.ID, a.tts.settingsHash(p)); err == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ready": true, "bytes": rec.Bytes, "cached": true, "timings": rec.Timings != "",
		})
		return
	}

	size, err := a.generateChapterAudio(novel, ch, p)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = a.store.AddHistory(u.ID, novel.ID, ch.ID, "listen", ch.Title)
	rec, _ := a.store.GetAudio(ch.ID, a.tts.settingsHash(p))
	writeJSON(w, http.StatusOK, map[string]any{
		"ready": true, "bytes": size, "cached": false, "timings": rec.Timings != "",
	})
}

// handleStreamAudio serves the cached WAV for the chapter under the currently
// configured provider, supporting range requests.
func (a *App) handleStreamAudio(w http.ResponseWriter, r *http.Request) {
	ch, err := a.store.GetChapter(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "chapter not found")
		return
	}
	p, perr := a.activeProvider()
	if perr != nil {
		writeErr(w, http.StatusNotFound, "no TTS model configured")
		return
	}
	rec, err := a.store.GetAudio(ch.ID, a.tts.settingsHash(p))
	if err != nil {
		writeErr(w, http.StatusNotFound, "audio not generated for the current voice model")
		return
	}
	abs, err := safeJoin(a.cfg.DataDir, rec.Path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "unsafe audio path")
		return
	}
	// #nosec G304 -- abs is produced by safeJoin, which rejects traversal.
	f, err := os.Open(abs)
	if err != nil {
		writeErr(w, http.StatusNotFound, "audio not generated yet")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not stat audio")
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, filepath.Base(abs), info.ModTime(), f)
}

// handleAudioTimings returns the word timings for the current provider's audio,
// or null when the provider gives none.
func (a *App) handleAudioTimings(w http.ResponseWriter, r *http.Request) {
	ch, err := a.store.GetChapter(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "chapter not found")
		return
	}
	p, perr := a.activeProvider()
	if perr != nil {
		writeJSON(w, http.StatusOK, map[string]any{"timings": nil})
		return
	}
	rec, err := a.store.GetAudio(ch.ID, a.tts.settingsHash(p))
	if err != nil || rec.Timings == "" {
		writeJSON(w, http.StatusOK, map[string]any{"timings": nil})
		return
	}
	var timings []WordTiming
	if err := json.Unmarshal([]byte(rec.Timings), &timings); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"timings": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"timings": timings})
}

func (a *App) handleDeleteAudio(w http.ResponseWriter, r *http.Request) {
	ch, err := a.store.GetChapter(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "chapter not found")
		return
	}
	p, perr := a.activeProvider()
	if perr != nil {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	rel, err := a.store.DeleteAudio(ch.ID, a.tts.settingsHash(p))
	if err == nil && rel != "" {
		if abs, jerr := safeJoin(a.cfg.DataDir, rel); jerr == nil {
			_ = os.Remove(abs)
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---------- WAV joining ----------

// joinWAV concatenates WAV files that share a format, producing one file.
func joinWAV(parts [][]byte) ([]byte, error) {
	if len(parts) == 0 {
		return nil, errors.New("no audio parts")
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	first, err := parseWAV(parts[0])
	if err != nil {
		return nil, err
	}
	var pcm bytes.Buffer
	pcm.Write(first.data)
	for _, p := range parts[1:] {
		w, err := parseWAV(p)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(w.fmt, first.fmt) {
			return nil, errors.New("audio chunks have differing formats")
		}
		pcm.Write(w.data)
	}
	return buildWAV(first.fmt, pcm.Bytes()), nil
}

type wavData struct {
	fmt  []byte
	data []byte
}

// wavDuration returns the play length of a WAV in seconds.
func wavDuration(w wavData) float64 {
	if len(w.fmt) < 12 {
		return 0
	}
	byteRate := binary.LittleEndian.Uint32(w.fmt[8:12])
	if byteRate == 0 {
		return 0
	}
	return float64(len(w.data)) / float64(byteRate)
}

func parseWAV(b []byte) (wavData, error) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return wavData{}, errors.New("invalid WAV data")
	}
	var out wavData
	i := 12
	for i+8 <= len(b) {
		id := string(b[i : i+4])
		size := int(binary.LittleEndian.Uint32(b[i+4 : i+8]))
		start := i + 8
		end := start + size
		if end > len(b) {
			end = len(b)
		}
		switch id {
		case "fmt ":
			out.fmt = b[start:end]
		case "data":
			out.data = b[start:end]
		}
		i = start + size
		if size%2 == 1 {
			i++ // chunks are word-aligned
		}
	}
	if out.fmt == nil {
		return wavData{}, errors.New("WAV missing fmt chunk")
	}
	return out, nil
}

func buildWAV(fmtChunk, data []byte) []byte {
	var buf bytes.Buffer
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(4+8+len(fmtChunk)+8+len(data)))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(len(fmtChunk)))
	buf.Write(fmtChunk)
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(len(data)))
	buf.Write(data)
	return buf.Bytes()
}
