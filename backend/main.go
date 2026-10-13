package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// App bundles the dependencies shared by the HTTP handlers.
type App struct {
	cfg   Config
	store *Store
	tts   *TTS
}

func main() {
	cfg := LoadConfig()

	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		log.Fatalf("cannot create data dir: %v", err)
	}

	store, err := OpenStore(filepath.Join(cfg.DataDir, "localread.db"))
	if err != nil {
		log.Fatalf("cannot open database: %v", err)
	}
	defer store.Close()

	if err := seedAdmin(store, cfg); err != nil {
		log.Fatalf("cannot seed admin account: %v", err)
	}
	if err := seedTTSProvider(store, cfg); err != nil {
		log.Fatalf("cannot seed TTS provider: %v", err)
	}

	app := &App{cfg: cfg, store: store, tts: NewTTS()}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           app.routes(),
		ReadHeaderTimeout: 15 * time.Second,
	}

	log.Printf("localread listening on %s", cfg.Addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// auth wraps a handler so it requires a valid session and receives the user.
func (a *App) auth(h http.HandlerFunc) http.Handler { return a.requireAuth(h) }

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "tts": a.ttsStatus()})
	})

	// auth
	mux.HandleFunc("POST /api/auth/register", a.handleRegister)
	mux.HandleFunc("POST /api/auth/login", a.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", a.handleLogout)
	mux.Handle("GET /api/auth/me", a.auth(a.handleMe))

	// novels & chapters
	mux.Handle("GET /api/novels", a.auth(a.handleListNovels))
	mux.Handle("POST /api/novels/import", a.auth(a.handleImport))
	mux.Handle("GET /api/novels/{id}", a.auth(a.handleGetNovel))
	mux.Handle("DELETE /api/novels/{id}", a.auth(a.handleDeleteNovel))
	mux.Handle("GET /api/chapters/{id}", a.auth(a.handleGetChapter))

	// tts / audio
	mux.Handle("GET /api/chapters/{id}/audio", a.auth(a.handleStreamAudio))
	mux.Handle("POST /api/chapters/{id}/audio", a.auth(a.handleGenerateAudio))
	mux.Handle("DELETE /api/chapters/{id}/audio", a.auth(a.handleDeleteAudio))
	mux.Handle("GET /api/chapters/{id}/audio/status", a.auth(a.handleChapterAudioStatus))
	mux.Handle("GET /api/chapters/{id}/audio/timings", a.auth(a.handleAudioTimings))

	// tts providers (models)
	mux.Handle("GET /api/tts/providers", a.auth(a.handleListTTSProviders))
	mux.Handle("POST /api/tts/providers", a.auth(a.handleCreateTTSProvider))
	mux.Handle("PUT /api/tts/providers/{id}", a.auth(a.handleUpdateTTSProvider))
	mux.Handle("DELETE /api/tts/providers/{id}", a.auth(a.handleDeleteTTSProvider))
	mux.Handle("POST /api/tts/providers/{id}/activate", a.auth(a.handleActivateTTSProvider))
	mux.Handle("POST /api/tts/providers/{id}/test", a.auth(a.handleTestTTSProvider))

	// catalog
	mux.Handle("GET /api/catalog/search", a.auth(a.handleCatalogSearch))
	mux.Handle("POST /api/catalog/install", a.auth(a.handleCatalogInstall))

	// progress & history
	mux.Handle("GET /api/progress", a.auth(a.handleListProgress))
	mux.Handle("GET /api/progress/{novelID}", a.auth(a.handleGetProgress))
	mux.Handle("PUT /api/progress/{novelID}", a.auth(a.handleSaveProgress))
	mux.Handle("GET /api/history", a.auth(a.handleListHistory))
	mux.Handle("POST /api/history", a.auth(a.handleAddHistory))

	// account
	mux.Handle("GET /api/account", a.auth(a.handleGetAccount))
	mux.Handle("PUT /api/account", a.auth(a.handleUpdateAccount))
	mux.Handle("PUT /api/account/password", a.auth(a.handleChangePassword))

	// unknown api routes -> json 404
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "not found")
	})

	// static SPA
	mux.Handle("/", a.spaHandler())

	return logRequests(mux)
}

// spaHandler serves the built React app and falls back to index.html so
// client-side routes work on hard refresh.
func (a *App) spaHandler() http.Handler {
	dir := findStaticDir()
	if dir == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{
				"service": "localread",
				"note":    "frontend build not found; run `npm run build` in frontend/ or use the vite dev server on :5173",
			})
		})
	}
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reject obvious traversal attempts early; FileServer also guards.
		if strings.Contains(r.URL.Path, "..") || strings.ContainsRune(r.URL.Path, 0) {
			writeErr(w, http.StatusBadRequest, "invalid path")
			return
		}
		clean := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(r.URL.Path, "/")))
		if info, err := os.Stat(clean); err == nil && !info.IsDir() {
			fs.ServeHTTP(w, r)
			return
		}
		// #nosec G304 G703 -- static dir is fixed and r.URL.Path is rejected
		// above when it contains ".."; ServeMux also cleans traversal.
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	})
}

// findStaticDir locates the built frontend relative to the working directory.
func findStaticDir() string {
	candidates := []string{}
	if v := os.Getenv("LOCALREAD_STATIC"); v != "" {
		candidates = append(candidates, v)
	}
	candidates = append(
		candidates,
		filepath.Join("frontend", "dist"),
		filepath.Join("..", "frontend", "dist"),
	)
	for _, c := range candidates {
		if info, err := os.Stat(filepath.Join(c, "index.html")); err == nil && !info.IsDir() {
			return c
		}
	}
	return ""
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			log.Printf(
				"%s %s -> %d (%s)",
				r.Method,
				r.URL.Path,
				sw.status,
				time.Since(start).Round(time.Millisecond),
			)
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
