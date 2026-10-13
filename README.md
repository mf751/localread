# localread

A local-first web reader: a **React** frontend and a **Go** backend that manages
your account, novels, chapters, reading progress and history, and generates
speech for chapters using a **locally running AI voice model**. You can add
several models (any OpenAI-compatible `/v1/audio/speech` server, e.g. LocalAI's
`qwen3-tts-cpp`, or Kokoro's captioned endpoint) on the **TTS** page and pick
which one to use. Models that return word timings get word-by-word highlighting
in the reader, and the page follows along by scrolling to keep the highlighted
word in view; models that only return audio just play. The UI is a left sidebar
shell with **light and dark themes** (remembered per browser).

```
localread/
  backend/    Go API + TTS proxy + storage (SQLite)
  frontend/   React (Vite + TypeScript) UI
```

## Backend

```sh
cd backend
go run .
```

Serves on `:8099` (override with `LOCALREAD_ADDR`). Data (SQLite DB, imported
novels, generated audio) lives in `./data` by default; override with
`LOCALREAD_DATA`.

### Environment

| Var | Default | Purpose |
| --- | --- | --- |
| `LOCALREAD_ADDR` | `:8099` | Listen address |
| `LOCALREAD_DATA` | `./data` | Data/cache directory |
| `LOCALREAD_ADMIN_PASSWORD` | `localread` | Seed admin password (see note below) |
| `TTS_BASE_URL` | `http://localhost:8880` | Default model base URL (Kokoro) |
| `TTS_MODEL` | `kokoro` | Default speech model |
| `TTS_VOICE` | `af_heart` | Default voice |
| `TTS_LANG` | `a` | Language code (Kokoro lang_code) |
| `TTS_SPEED` | `1.0` | Speaking rate |

The `TTS_*` values seed the **first** model on an empty database and make it
active; after that, add and switch models on the **TTS** page (stored in
SQLite, no restart needed).

A default admin account (`admin` / `localread`) is seeded on first run. **This
default password is intentional** — it is a local, single-machine app. Override
it with `LOCALREAD_ADMIN_PASSWORD` for anything non-local.

### Import formats

- `.txt` — split into chapters on `Chapter N` / `CHAPTER I` style headings, or
  on blank-line-separated blocks when no headings are found.
- `.epub` — parsed via `container.xml` → OPF spine → XHTML, chapters taken from
  heading/section structure.

## TTS models

Open the **TTS** page to list your models and add a new one (name, base URL,
optional API key, model, voice, speed, language). You choose which model is in
use; the backend sends all speech to it.

Enter the **server root** as the base URL (e.g. `http://localhost:8080`, not
`http://localhost:8080/v1/audio/speech`). If you paste a full endpoint anyway the
backend strips the known suffix (`/v1/audio/speech`, `/v1/audio`, `/v1`,
`/dev/captioned_speech`) so it still works.

When a model is saved or tested the backend probes it:

- **Kokoro** (`/dev/captioned_speech`) returns the audio **and** per-word
  timestamps, so the reader highlights words as they are spoken.
- A plain OpenAI-compatible server (LocalAI, etc.) only returns audio; the
  reader just plays it.

If the probe fails, the TTS page shows the provider's own error message (wrong
URL, rejected voice, model still loading, …) instead of a generic “no response”.
For LocalAI's `qwen3-tts-cpp` the voice must be a voice profile,
`localai://voice-profiles/<id>`, not a generic name like `default`.

The frontend never talks to a TTS server directly — it always asks the backend,
which also caches the result.

## Speech (LocalAI) troubleshooting

If "Generate speech" fails with:

```
tts returned 500: {"error":{"code":500,"message":"rpc error: code = Unavailable
desc = error reading from server: EOF","type":""}}
```

the local voice backend crashed, not the reader. The usual cause on a small GPU
(e.g. 4 GB GTX 1650) is that LocalAI's `qwen3-tts-cpp` backend offloads every
layer and runs out of VRAM during synthesis:

```
ggml_backend_cuda_buffer_type_alloc_buffer: ... cudaMalloc failed: out of memory
SIGSEGV: segmentation violation
```

Fix: let the backend borrow system RAM with `GGML_CUDA_ENABLE_UNIFIED_MEMORY=1`.
The backend is launched via its `run.sh` inside the container, so:

```sh
scripts/fix-localai-tts.sh          # patches /backends/cuda13-qwen3-tts-cpp/run.sh
curl -s -X POST http://localhost:8080/backend/shutdown \
  -H 'Content-Type: application/json' -d '{"model":"qwen3-tts-cpp"}'   # force a reload
```

Re-run the script after installing or upgrading the backend (an upgrade replaces
`run.sh`). The permanent alternative is to recreate the LocalAI container with
`-e GGML_CUDA_ENABLE_UNIFIED_MEMORY=1` added to its `docker run` command.

You can confirm the backend state at
`GET /api/backend-logs/qwen3-tts-cpp`; a healthy load prints
`[Load] Talker backend: CUDA0` and generation finishes with `[Perf] Total … RTF < 1`.

## Frontend

```sh
cd frontend
npm install
npm run dev
```

Dev server on `:5173`, proxies `/api` to the backend on `:8099`.

## Run with Docker

The image builds the React UI and the Go server and serves both from a single
process.

```sh
docker run --name localread --network host mf751/localread:latest
```

Then open <http://localhost:8080> (the image listens on `:8080` by default).

`--network host` shares your machine's network stack with the container, so the
speech server is reachable at the *same* address it has on your host. It expects
**Kokoro to be running at <http://localhost:8880>**, which is what the seeded
model already points at; if your Kokoro is somewhere else, edit that model's base
URL on the **TTS** page.

To keep your library across container replacements, mount the data volume:

```sh
docker run --name localread --network host \
  -v localread-data:/data \
  mf751/localread:latest
```

Host networking is **Linux-only** and ignores `-p`; the app listens on `:8080`
unless you override it (e.g. `-e LOCALREAD_ADDR=127.0.0.1:8099` to change the port
and keep it off the LAN).

A `docker-compose.yml` is included in the repo; `docker compose up -d` runs the
same command.

### Without host networking

If you do not want to give the container `--network host`, run the reader on a
normal published port, put it on a Docker network together with the speech
container, and point the model at that container's name in the UI:

```sh
docker network create localread

docker run -d --name localread \
  -p 127.0.0.1:8080:8080 \
  -v localread-data:/data \
  --network localread \
  mf751/localread:latest

# join your already-running speech container to the same network
docker network connect localread kokoro-tts
```

On the **TTS** page change the model base URL from `http://localhost:8880` to
`http://kokoro-tts:8880`. Inside a container `localhost` is the container itself,
so the host URL cannot work here: the two containers must share a network and
talk by container name. (Alternatively, publish the speech server on the host's
`0.0.0.0` and use `http://host.docker.internal:8880` with
`--add-host=host.docker.internal:host-gateway`.)

## Security notes

All filesystem reads/writes for user-supplied content go through
`safeJoin(root, rel)` in `backend/safeserve.go`, which rejects traversal
(`..`), absolute paths, and NUL bytes, and re-verifies containment after
resolution. Generated audio is cached under the data root and served with
`http.ServeContent` (with range support). The audio cache is keyed by the voice
model settings (provider + base URL + model + voice + speed + language), so
changing the model regenerates into a separate file rather than reusing or
overwriting another model's audio; Generate reuses a cached file when one exists
for the current settings. This is handled entirely in the backend — the frontend
only calls the reader's `/api/chapters/{id}/audio` endpoints.
