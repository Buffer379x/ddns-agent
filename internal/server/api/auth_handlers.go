package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"ddns-agent/internal/auth"
	"ddns-agent/internal/database"
	"ddns-agent/internal/netutil"
)

var dummyPasswordHash = func() string { hash, _ := auth.HashPassword("invalid-login-timing-placeholder"); return hash }()

func (h *Handler) Setup(w http.ResponseWriter, r *http.Request) {
	writeError(w, 410, "initial credentials are in the local bootstrap-password file")
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if readJSON(r, &req) != nil || len(req.Username) > 256 || len(req.Password) > 72 {
		writeError(w, 400, "invalid credentials")
		return
	}
	if !h.loginAccounts.Allow(strings.ToLower(req.Username)) || !h.loginGlobal.Allow("login") {
		writeError(w, 429, "rate limit exceeded")
		return
	}
	user, err := h.db.GetUserByUsername(req.Username)
	if err != nil {
		_ = auth.CheckPassword(dummyPasswordHash, req.Password)
		writeError(w, 401, "invalid credentials")
		return
	}
	if !auth.CheckPassword(user.PasswordHash, req.Password) {
		h.log.Warn("auth", "failed login for %q", netutil.Redact(req.Username))
		writeError(w, 401, "invalid credentials")
		return
	}
	if auth.CheckPassword(user.PasswordHash, "admin") {
		if err = h.db.RequirePasswordChange(user.ID); err != nil {
			writeError(w, 500, "cannot update account")
			return
		}
		user.MustChangePassword = true
	}
	h.issueLogin(w, r, user)
}

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	sub, _ := claims["sub"].(float64)
	user, err := h.db.GetUserByID(int(sub))
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (h *Handler) issueLogin(w http.ResponseWriter, r *http.Request, user *database.User) {
	token, err := h.auth.GenerateToken(user.ID, user.Username, user.Role)
	if err != nil {
		writeError(w, 500, "cannot create session")
		return
	}
	auth.SetCookie(w, r, token)
	writeJSON(w, 200, map[string]any{"token": token, "user": user, "password_is_default": user.MustChangePassword})
}
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if err := h.auth.Logout(auth.RequestToken(r)); err != nil {
		writeError(w, 500, "cannot revoke session")
		return
	}
	auth.ClearCookie(w, r)
	writeJSON(w, 200, map[string]string{"status": "signed out"})
}
func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current  string `json:"current_password"`
		Password string `json:"password"`
	}
	if readJSON(r, &req) != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	claims := auth.GetUserFromContext(r)
	id, _ := claims["sub"].(float64)
	user, err := h.db.GetUserByID(int(id))
	if err != nil || !auth.CheckPassword(user.PasswordHash, req.Current) {
		writeError(w, 403, "current password is incorrect")
		return
	}
	if req.Password == req.Current {
		writeError(w, 400, "choose a different password")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, 500, "hashing password")
		return
	}
	if err = h.db.UpdateUser(user.ID, user.Username, hash, user.Role); err != nil {
		writeError(w, 500, "cannot change password")
		return
	}
	if user.MustChangePassword {
		_ = os.Remove(filepath.Join(h.cfg.DataDir, "bootstrap-password"))
	}
	user.MustChangePassword = false
	h.issueLogin(w, r, user)
}
