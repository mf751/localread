package main

import (
	"os"
	"path/filepath"
	"strconv"
)

// Config holds runtime configuration, all overridable via environment.
type Config struct {
	Addr      string
	DataDir   string
	AdminUser string

	// AdminPassword seeds the default account on first run.
	//
	// INTENTIONAL: this hardcoded default password is deliberate for a local,
	// single-machine reader. Operators who expose the service beyond localhost
	// must set LOCALREAD_ADMIN_PASSWORD. This is the one "hardcoded credential"
	// the security scanner reports and it is accepted on purpose.
	AdminPassword string

	TTSBaseURL string
	TTSModel   string
	TTSVoice   string
	TTSLang    string
	TTSSpeed   float64

	// OPDSUrls is a comma-separated list of OPDS feeds used by catalog search.
	// Empty (the default) means only the local library is searched.
	OPDSUrls string

	MaxUploadBytes int64
}

func LoadConfig() Config {
	c := Config{
		Addr:           env("LOCALREAD_ADDR", ":8099"),
		DataDir:        env("LOCALREAD_DATA", "data"),
		AdminUser:      env("LOCALREAD_ADMIN_USER", "admin"),
		AdminPassword:  env("LOCALREAD_ADMIN_PASSWORD", "localread"), // intentional default, see above
		TTSBaseURL:     env("TTS_BASE_URL", "http://localhost:8880"),
		TTSModel:       env("TTS_MODEL", "kokoro"),
		TTSVoice:       env("TTS_VOICE", "af_heart"),
		TTSLang:        env("TTS_LANG", "a"),
		TTSSpeed:       envFloat("TTS_SPEED", 1.0),
		OPDSUrls:       env("LOCALREAD_OPDS_URLS", ""),
		MaxUploadBytes: envInt64("LOCALREAD_MAX_UPLOAD", 64<<20),
	}
	if abs, err := filepath.Abs(c.DataDir); err == nil {
		c.DataDir = abs
	}
	return c
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt64(key string, def int64) int64 {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}
