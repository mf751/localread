package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const sessionCookie = "localread_session"
const sessionTTL = 30 * 24 * time.Hour

type ctxKey string

const userCtxKey ctxKey = "user"

func hashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

func checkPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// seedAdmin creates the default account on an empty database.
func seedAdmin(s *Store, cfg Config) error {
	n, err := s.UserCount()
	if err != nil || n > 0 {
		return err
	}
	hash, err := hashPassword(cfg.AdminPassword)
	if err != nil {
		return err
	}
	_, err = s.CreateUser(cfg.AdminUser, hash, "Local Reader")
	return err
}

func (a *App) handleRegister(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"displayName"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	if err := validateUsername(in.Username); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(in.Password) < 6 {
		writeErr(w, http.StatusBadRequest, "password must be at least 6 characters")
		return
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not hash password")
		return
	}
	u, err := a.store.CreateUser(in.Username, hash, in.DisplayName)
	if err != nil {
		writeErr(w, http.StatusConflict, "username already taken")
		return
	}
	if err := a.startSession(w, u.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not start session")
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	u, hash, err := a.store.UserByUsername(in.Username)
	if err != nil || !checkPassword(hash, in.Password) {
		// Same message for both cases to avoid user enumeration.
		writeErr(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if err := a.startSession(w, u.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not start session")
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = a.store.DeleteSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) handleMe(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	writeJSON(w, http.StatusOK, u)
}

func (a *App) startSession(w http.ResponseWriter, userID int64) error {
	token := randomToken()
	if err := a.store.CreateSession(token, userID, sessionTTL); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// requireAuth rejects unauthenticated requests with 401.
func (a *App) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, err := a.userFromRequest(r)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userCtxKey, u)))
	})
}

func (a *App) userFromRequest(r *http.Request) (User, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		// Also permit a bearer token for API clients.
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			return a.store.UserBySession(strings.TrimPrefix(h, "Bearer "))
		}
		return User{}, errors.New("no session")
	}
	return a.store.UserBySession(c.Value)
}

func userFrom(ctx context.Context) User {
	u, _ := ctx.Value(userCtxKey).(User)
	return u
}

func validateUsername(s string) error {
	if len(s) < 3 || len(s) > 32 {
		return errors.New("username must be 3-32 characters")
	}
	for _, r := range s {
		if !(r == '_' || r == '-' || r == '.' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return errors.New("username may only contain letters, digits, '_', '-' and '.'")
		}
	}
	return nil
}

// ---------- small HTTP helpers ----------

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
