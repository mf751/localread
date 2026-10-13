# syntax=docker/dockerfile:1

# --- 1. React build ---------------------------------------------------------
FROM node:22-alpine AS frontend-builder
WORKDIR /app/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# --- 2. Go build ------------------------------------------------------------
# go.mod requires go >= 1.27.2; pin the exact toolchain so the builder never has
# to download one at build time. CGO is off (modernc.org/sqlite is pure Go), so
# the result is a static binary.
FROM golang:1.27.2-alpine AS backend-builder
WORKDIR /app/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" -o /app/server .

# --- 3. Minimal runtime image ----------------------------------------------
FROM alpine:3.22
# ca-certificates: catalog search fetches OPDS feeds over HTTPS.
# busybox wget: used by the healthcheck below.
RUN apk add --no-cache ca-certificates \
 && adduser -D -u 1000 app

WORKDIR /app
COPY --from=backend-builder /app/server ./server
COPY --from=frontend-builder /app/frontend/dist ./frontend/dist

# SQLite database and generated audio live here; mount a volume to persist them.
RUN mkdir -p /data && chown -R app:app /data /app
USER app

# The Go server serves both the API (/api/*) and the built SPA on one port, so
# only this port is ever published — the backend is not exposed separately.
ENV LOCALREAD_ADDR=:8080 \
    LOCALREAD_DATA=/data \
    LOCALREAD_STATIC=/app/frontend/dist
EXPOSE 8080
VOLUME ["/data"]

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/api/health >/dev/null 2>&1 || exit 1

CMD ["./server"]
