package main

import (
	"net/http"
	"strings"
)

func (a *App) handleGetAccount(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	stats, err := a.store.Stats(u.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load stats")
		return
	}
	audio := a.ttsStatus()
	writeJSON(w, http.StatusOK, map[string]any{
		"user":  u,
		"stats": stats,
		"tts":   audio,
	})
}

func (a *App) handleUpdateAccount(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	var in struct {
		DisplayName string `json:"displayName"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	if len(in.DisplayName) > 80 {
		writeErr(w, http.StatusBadRequest, "display name too long")
		return
	}
	if err := a.store.UpdateUserProfile(u.ID, in.DisplayName); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not update profile")
		return
	}
	updated, _ := a.store.UserByID(u.ID)
	writeJSON(w, http.StatusOK, updated)
}

func (a *App) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	var in struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	_, hash, err := a.store.UserByUsername(u.Username)
	if err != nil || !checkPassword(hash, in.CurrentPassword) {
		writeErr(w, http.StatusForbidden, "current password is incorrect")
		return
	}
	if len(in.NewPassword) < 6 {
		writeErr(w, http.StatusBadRequest, "new password must be at least 6 characters")
		return
	}
	nh, err := hashPassword(in.NewPassword)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not hash password")
		return
	}
	if err := a.store.SetPassword(u.ID, nh); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not update password")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
