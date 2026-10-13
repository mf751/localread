# localread — implementation notes

## Request
A local web UI (React) for account, novels, search/install, upload, reading
progress and history of novels/chapters, talking to a Go backend that uses a
locally run AI voice model to synthesize chapter speech.
Also: "fix the traversal error, only the password one is intentional."

There was no existing code in the directory (only `mkdir localread` in shell
history), so the whole stack was scaffolded with safe path handling built in.

## Layout
```
backend/
  main.go            HTTP server, routes, SPA serving, request log
  config.go          env config incl. the intentional admin password
  auth.go            bcrypt, sessions (cookie or bearer), middleware
  store.go           SQLite schema + queries (users/novels/chapters/...)
  novels.go          library CRUD, import (multipart/JSON)
  importer.go        txt + epub parsing (zip-slip guarded)
  progress.go        progress + history handlers
  catalog.go         search/install (OPDS sources + local library)
  account.go         profile, password, stats
  tts.go             TTS client, chunking, WAV merge, timing alignment, cache
  tts_providers.go   TTS model (provider) CRUD, probing, active selection
  safeserve.go       safeJoin(): the single traversal choke point
  *_test.go          unit + end-to-end integration tests
frontend/
  src/api.ts         typed API client
  src/auth.tsx       auth context
  src/theme.ts       light/dark theme state (localStorage + OS preference)
  src/App.tsx        routes + RequireAuth
  src/pages/*        Login, Library, Novel, Reader, Catalog, History, TTS, Account
  src/components/*   Layout (sidebar shell), AudioPlayer
```

## How the pieces satisfy the request
- **Account**: register/login/logout, profile edit, password change, stats.
- **Novels + upload**: upload `.txt`/`.epub` (or paste text); parsed into chapters.
- **Searching/installing**: `/catalog/search` searches the local library and any
  `LOCALREAD_OPDS_URLS` OPDS feeds; `/catalog/install` downloads + imports.
- **Progress + history**: scroll position saved per novel/chapter, plus an
  activity log (`open`/`finish`/`listen`/`import`).
- **AI voice**: models are managed on the **TTS** page (`/api/tts/providers`).
  `POST /api/chapters/{id}/audio` chunks the chapter, calls the *active* model,
  merges the WAV chunks, caches them; GET streams with HTTP range support. The
  cache is keyed by the model settings (see below), so the frontend never needs
  to know which model produced a file.
- **Word highlighting**: models that return timings (Kokoro's captioned
  endpoint) get their per-word timestamps stored and served from
  `/api/chapters/{id}/audio/timings`; the reader aligns them to the rendered
  words and highlights as the audio plays, and auto-scrolls to keep the
  highlighted word in view (only nudging once the word leaves a comfortable band,
  so it steps rather than jitters). Models without timings simply play.

## Interface + theming
The app shell is a **left sidebar** (`Layout.tsx`): brand, vertical nav links
(inline SVG icons), and a footer with a light/dark segmented toggle, the user
chip and sign-out. Every authenticated route — including the chapter reader —
renders inside this shell. The sidebar has a **collapse toggle** in its header:
expanded it is 264px with labels, collapsed it is a 76px icon-only rail; the
state is persisted in `localStorage["localread-sidebar-collapsed"]`. Under
`900px` the sidebar reflows to a horizontal, icon-only top bar (and the collapse
toggle is hidden).

The reader keeps its own layout inside the shell: `Layout` adds a
`reader-content` class to `.content` for the reader route, which drops the
page padding/max-width so the reader can centre its prose column (780px) in the
remaining space. The reader's back-link + progress bar stay sticky at the top.

Themes are CSS custom properties: `:root` holds the light tokens and
`[data-theme="dark"]` overrides them (`styles.css`). `theme.ts` holds the state:
it defaults to the OS `prefers-color-scheme`, follows it until the user picks a
side, then persists the choice in `localStorage`; an inline script in
`index.html` sets `data-theme` before first paint to avoid a flash. Panels,
borders, accents and shadows (`--shadow-sm/md/lg`, `--shadow-accent`) are all
tokens, so a theme is just a variable block. The frontend never sends the theme
to the backend.

## TTS models (providers)
The `tts_providers` table holds each model (name, base URL, API key, model,
voice, speed, language) plus a tri-state `timings` column (`-1` unknown, `0` no,
`1` yes). The active model id lives in the `settings` key/value table. On an
empty database the env `TTS_*` values seed the first provider (default: Kokoro
at `http://localhost:8880`).

Adding or testing a model probes it: the backend first tries
`POST /dev/captioned_speech` (Kokoro-style JSON with base64 WAV + timestamps),
and falls back to `POST /v1/audio/speech` (raw audio). The result is stored so
later generations know whether timings are available. When a probe fails the
provider's own error is returned as `detail` (create/update/test all share the
same `{provider, reachable, timings, detail}` envelope) so the UI can explain the
failure instead of saying “no response”. The base URL is normalized on save: a
pasted endpoint (`/v1/audio/speech`, `/v1/audio`, `/v1`, `/dev/captioned_speech`)
is stripped back to the server root. Generation also re-detects lazily and
persists the result.

`alignTokens` maps the backend's spoken tokens back to the displayed source
words (numbers/currency expand into several tokens), emitting exactly one timing
per source word; words the model did not speak become zero-length markers so the
reader keeps the previous word highlighted through pauses.

## Speech cache keying (backend-only)
Generated speech is cached per `(chapter, model settings)`. The settings hash is
`providerID | baseURL | model | voice | speed | lang | v2`
(`TTS.settingsHash()`), and the cache file lives at
`audio/<novelID>/<chapterID>/<settingsHash>.wav`. The `audio` table's primary key
is `(chapter_id, settings_hash)` and it also stores the word timings JSON, so:

- Pressing **Generate** first looks up the row for the *current* settings; a hit
  is returned immediately (`"cached": true`) without calling the model.
- Switching model makes a different hash, so Generate regenerates into a new
  file and leaves the other model's cached audio untouched; switching back
  reuses the earlier file.
- Streaming and Regenerate resolve the file through the same lookup, so they
  always target the audio that matches the running configuration.
- When the model changes, `handleStreamAudio` returns 404 until the chapter is
  generated again for the new settings.

All of this is internal to the Go backend; the React frontend only calls
`/api/chapters/{id}/audio` (POST/GET/DELETE), `/audio/status`,
`/audio/timings` and `/api/tts/providers`, and never talks to a model directly.

## Traversal handling (the security ask)
`safeJoin` rejects `..`, absolute/drive-letter paths and NUL bytes, cleans the
join, and confirms containment via `filepath.Rel`. Ids are validated with
`safeID`. Audio cache paths are built only from validated ids. Static SPA
serving rejects `..` (and ServeMux cleans anyway). Covered by tests; `go vet`
and `go test ./...` are clean.

The hardcoded default admin password is single, explicit and commented as
intentional in `config.go`.

## LocalAI TTS on small GPUs (operational)
The reader was returning `502 synthesis failed ... tts returned 500 ... EOF` for
longer chapters. Root cause was in LocalAI, not the reader: the installed
`cuda13-qwen3-tts-cpp` backend hardcodes full GPU offload (its `n_gpu_layers` is
not configurable; model fields like `gpu_layers`, `low_vram`, `context_size` are
ignored) and OOMs on a 4 GB card:
`ggml_backend_cuda_buffer_type_alloc_buffer ... cudaMalloc failed: out of memory`
followed by `SIGSEGV`. Fix applied: `GGML_CUDA_ENABLE_UNIFIED_MEMORY=1` exported
in the backend's `run.sh`, so it falls back to system RAM.
See `scripts/fix-localai-tts.sh` and the README troubleshooting section.

