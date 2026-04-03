package api

import (
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ddns-agent/internal/auth"
	"ddns-agent/internal/backup"
	"ddns-agent/internal/config"
	"ddns-agent/internal/crypto"
	"ddns-agent/internal/database"
	"ddns-agent/internal/logger"
	"ddns-agent/internal/provider"
	"ddns-agent/internal/provider/constants"
	"ddns-agent/internal/server/sse"
	"ddns-agent/internal/updater"
	"ddns-agent/internal/webhook"
	"github.com/go-chi/chi/v5"
)

type Handler struct {
	db        *database.DB
	auth      *auth.Service
	updater   *updater.Service
	webhooks  *webhook.Service
	backup    *backup.Service
	encryptor *crypto.Encryptor
	sse       *sse.Broker
	log       *logger.Logger
	webFS     fs.FS
	version   string
	cfg       *config.Config
}

func NewHandler(
	db *database.DB, authSvc *auth.Service, updaterSvc *updater.Service,
	webhookSvc *webhook.Service, backupSvc *backup.Service,
	encryptor *crypto.Encryptor, sseBroker *sse.Broker, log *logger.Logger,
	webFS fs.FS, version string, cfg *config.Config,
) *Handler {
	return &Handler{
		db: db, auth: authSvc, updater: updaterSvc, webhooks: webhookSvc,
		backup: backupSvc, encryptor: encryptor, sse: sseBroker, log: log,
		webFS: webFS, version: version, cfg: cfg,
	}
}

func AdminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := auth.GetUserFromContext(r)
		if claims == nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		role, _ := claims["role"].(string)
		if role != "admin" {
			writeError(w, http.StatusForbidden, "admin access required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requestIsAdmin(r *http.Request) bool {
	claims := auth.GetUserFromContext(r)
	if claims == nil {
		return false
	}
	role, _ := claims["role"].(string)
	return role == "admin"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func paramInt(r *http.Request, name string) int {
	v, _ := strconv.Atoi(chi.URLParam(r, name))
	return v
}

func queryInt(r *http.Request, name string, fallback int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return fallback
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return i
}

// --- Version ---

func (h *Handler) Version(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": h.version})
}

// --- Health ---

func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	if err := h.db.Ping(); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unhealthy", "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "healthy"})
}

// --- Auth ---

func (h *Handler) Setup(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusGone, "setup endpoint removed, default admin is admin/admin")
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	user, err := h.db.GetUserByUsername(req.Username)
	if err != nil || !auth.CheckPassword(user.PasswordHash, req.Password) {
		h.log.Warn("auth", "failed login attempt for user: %s", req.Username)
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	token, _ := h.auth.GenerateToken(user.ID, user.Username, user.Role)
	passwordIsDefault := auth.CheckPassword(user.PasswordHash, "admin")
	h.log.Info("auth", "user '%s' logged in successfully", user.Username)
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "user": user, "password_is_default": passwordIsDefault})
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

// --- Records ---

// decryptProviderConfig returns plaintext JSON for the API client. If decryption fails, s is returned as-is (legacy plaintext rows).
func (h *Handler) decryptProviderConfig(s string) string {
	if s == "" || h.encryptor == nil {
		return s
	}
	dec, err := h.encryptor.Decrypt(s)
	if err != nil {
		return s
	}
	return dec
}

func (h *Handler) ListRecords(w http.ResponseWriter, r *http.Request) {
	records, err := h.db.ListRecords()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if records == nil {
		records = []database.Record{}
	}
	// Decrypt provider_config only for admins (editing). Viewers keep ciphertext in API responses.
	for i := range records {
		if requestIsAdmin(r) {
			records[i].ProviderConfig = h.decryptProviderConfig(records[i].ProviderConfig)
		}
	}
	writeJSON(w, http.StatusOK, records)
}

func (h *Handler) CreateRecord(w http.ResponseWriter, r *http.Request) {
	var rec database.Record
	if err := readJSON(r, &rec); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if h.encryptor != nil && rec.ProviderConfig != "" {
		encrypted, err := h.encryptor.Encrypt(rec.ProviderConfig)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encrypting config")
			return
		}
		rec.ProviderConfig = encrypted
	}
	id, err := h.db.CreateRecord(&rec)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rec.ID = int(id)
	h.log.Success("api", "record created: %s.%s (%s)", rec.Owner, rec.Domain, rec.Provider)
	writeJSON(w, http.StatusCreated, rec)
}

func (h *Handler) UpdateRecord(w http.ResponseWriter, r *http.Request) {
	id := paramInt(r, "id")
	existing, err := h.db.GetRecord(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "record not found")
		return
	}
	var rec database.Record
	if err := readJSON(r, &rec); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	rec.ID = existing.ID
	if rec.ProviderConfig == "" {
		rec.ProviderConfig = existing.ProviderConfig
	} else if h.encryptor != nil {
		encrypted, err := h.encryptor.Encrypt(rec.ProviderConfig)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encrypting config")
			return
		}
		rec.ProviderConfig = encrypted
	}
	if err := h.db.UpdateRecord(&rec); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (h *Handler) DeleteRecord(w http.ResponseWriter, r *http.Request) {
	id := paramInt(r, "id")
	if err := h.db.DeleteRecord(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) RefreshRecord(w http.ResponseWriter, r *http.Request) {
	id := paramInt(r, "id")
	if err := h.updater.ForceUpdateRecord(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "refreshed"})
}

func (h *Handler) RefreshAll(w http.ResponseWriter, r *http.Request) {
	h.log.Info("updater", "manual refresh of all active records initiated")
	go func() {
		errs := h.updater.ForceUpdate()
		if len(errs) > 0 {
			h.log.Warn("updater", "refresh completed with %d error(s)", len(errs))
		} else {
			h.log.Success("updater", "refresh of all active records completed")
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "refresh initiated"})
}

// --- Providers ---

func (h *Handler) ListProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, provider.AllProviderInfos())
}

func (h *Handler) ProviderFields(w http.ResponseWriter, r *http.Request) {
	name := constants.Provider(chi.URLParam(r, "name"))
	info := provider.GetProviderInfo(name)
	writeJSON(w, http.StatusOK, info)
}

// --- Logs ---

func (h *Handler) ListLogs(w http.ResponseWriter, r *http.Request) {
	level := r.URL.Query().Get("level")
	limit := queryInt(r, "limit", 100)
	offset := queryInt(r, "offset", 0)
	logs, total, err := h.db.ListLogs(level, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if logs == nil {
		logs = []database.LogEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": logs, "total": total})
}

func (h *Handler) DeleteLogs(w http.ResponseWriter, r *http.Request) {
	if err := h.db.DeleteLogs(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "logs cleared"})
}

// --- Settings ---

func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := h.db.AllSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (h *Handler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var settings map[string]string
	if err := readJSON(r, &settings); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	for k, v := range settings {
		if err := h.db.SetSetting(k, v); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if _, ok := settings["app_timezone"]; ok {
		h.syncAppTimezone(settings["app_timezone"])
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *Handler) syncAppTimezone(value string) {
	if h.cfg == nil {
		return
	}
	value = strings.TrimSpace(value)
	var loc *time.Location
	if value == "" {
		loc = h.cfg.TimeLocation()
	} else {
		var err error
		loc, err = time.LoadLocation(value)
		if err != nil {
			h.log.Warn("api", "invalid app_timezone %q: %v", value, err)
			return
		}
	}
	h.log.SetTimeLocation(loc)
	h.log.SetFileLogLocation(loc)
}

// --- Users ---

func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.db.ListUsers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if users == nil {
		users = []database.User{}
	}
	writeJSON(w, http.StatusOK, users)
}

func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := readJSON(r, &req); err != nil || req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password required")
		return
	}
	if req.Role == "" {
		req.Role = "viewer"
	}
	hash, _ := auth.HashPassword(req.Password)
	user, err := h.db.CreateUser(req.Username, hash, req.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

func (h *Handler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	id := paramInt(r, "id")
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var hash string
	if req.Password != "" {
		hash, _ = auth.HashPassword(req.Password)
	}
	if err := h.db.UpdateUser(id, req.Username, hash, req.Role); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *Handler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	id := paramInt(r, "id")
	if id == 1 {
		writeError(w, http.StatusForbidden, "the default admin user cannot be deleted")
		return
	}
	count, _ := h.db.UserCount()
	if count <= 1 {
		writeError(w, http.StatusConflict, "cannot delete last user")
		return
	}
	if err := h.db.DeleteUser(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- Webhooks ---

func (h *Handler) ListWebhooks(w http.ResponseWriter, r *http.Request) {
	hooks, err := h.db.ListWebhooks()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if hooks == nil {
		hooks = []database.Webhook{}
	}
	writeJSON(w, http.StatusOK, hooks)
}

func (h *Handler) CreateWebhook(w http.ResponseWriter, r *http.Request) {
	var hook database.Webhook
	if err := readJSON(r, &hook); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id, err := h.db.CreateWebhook(&hook)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	hook.ID = int(id)
	writeJSON(w, http.StatusCreated, hook)
}

func (h *Handler) UpdateWebhook(w http.ResponseWriter, r *http.Request) {
	id := paramInt(r, "id")
	var hook database.Webhook
	if err := readJSON(r, &hook); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	hook.ID = id
	if err := h.db.UpdateWebhook(&hook); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, hook)
}

func (h *Handler) DeleteWebhook(w http.ResponseWriter, r *http.Request) {
	id := paramInt(r, "id")
	if err := h.db.DeleteWebhook(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) TestWebhook(w http.ResponseWriter, r *http.Request) {
	id := paramInt(r, "id")
	hook, err := h.db.GetWebhook(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "webhook not found")
		return
	}
	if err := h.webhooks.SendTest(*hook); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "test sent"})
}

// --- Config Export/Import ---

func (h *Handler) ExportConfig(w http.ResponseWriter, r *http.Request) {
	data, err := h.backup.Export()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=ddns-agent-config.json")
	w.Write(data)
}

func (h *Handler) ImportConfig(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
	defer r.Body.Close()
	if err != nil {
		writeError(w, http.StatusBadRequest, "reading body: "+err.Error())
		return
	}
	if err := h.backup.Import(body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s, err := h.db.GetSetting("app_timezone"); err == nil {
		h.syncAppTimezone(s)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "imported"})
}

// --- Status ---

func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	total, active, errors, err := h.db.RecordCounts()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total_records":  total,
		"active_records": active,
		"error_records":  errors,
	})
}

// --- SSE ---

func (h *Handler) SSEEvents(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token != "" {
		if _, err := h.auth.ValidateToken(token); err != nil {
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}
	}
	h.sse.Handler()(w, r)
}

// --- Languages ---

func (h *Handler) ListLanguages(w http.ResponseWriter, r *http.Request) {
	entries, err := fs.ReadDir(h.webFS, "lang")
	if err != nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	var langs []map[string]string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		code := strings.TrimSuffix(e.Name(), ".json")
		langs = append(langs, map[string]string{
			"code": code,
			"name": languageName(code),
		})
	}
	if langs == nil {
		langs = []map[string]string{}
	}
	writeJSON(w, http.StatusOK, langs)
}

func (h *Handler) GetLanguage(w http.ResponseWriter, r *http.Request) {
	locale := chi.URLParam(r, "locale")
	locale = strings.ReplaceAll(locale, "/", "")
	locale = strings.ReplaceAll(locale, "..", "")

	data, err := fs.ReadFile(h.webFS, "lang/"+locale+".json")
	if err != nil {
		writeError(w, http.StatusNotFound, "language not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func languageName(code string) string {
	names := map[string]string{
		"en": "English",
		"de": "Deutsch",
		"fr": "Français",
		"es": "Español",
		"it": "Italiano",
		"pt": "Português",
		"nl": "Nederlands",
		"pl": "Polski",
		"ru": "Русский",
		"ja": "日本語",
		"zh": "中文",
		"ko": "한국어",
	}
	if name, ok := names[code]; ok {
		return name
	}
	return code
}
