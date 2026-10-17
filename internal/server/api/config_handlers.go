package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"ddns-agent/internal/netutil"
	"ddns-agent/internal/validation"
)

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
	data, password, err := importBody(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	mode := r.URL.Query().Get("records")
	if mode == "" {
		mode = "merge"
	}
	err = h.updater.Exclusive(func() error { return h.backup.ImportWithPassword(data, mode, password) })
	if err != nil {
		writeError(w, 400, netutil.Redact(err.Error()))
		return
	}
	settings, err := h.db.AllSettings()
	if err != nil {
		writeError(w, 500, "cannot reload settings")
		return
	}
	h.applySettings(settings)
	writeJSON(w, 200, map[string]string{"status": "imported"})
}

func (h *Handler) GetRecord(w http.ResponseWriter, r *http.Request) {
	rec, err := h.db.GetRecord(paramInt(r, "id"))
	if err != nil {
		writeError(w, 404, "record not found")
		return
	}
	plain, err := h.encryptor.OpenConfig(rec.ProviderConfig)
	if err != nil {
		writeError(w, 409, "credentials cannot be decrypted; restore the original key")
		return
	}
	rec.HasCredentials = true
	rec.ProviderConfig = validation.RedactedConfig(plain, rec.Provider)
	writeJSON(w, 200, rec)
}
func webhookDisplay(raw, typ string) string {
	if typ == "telegram" {
		return "Telegram · ••••••••"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "••••••••"
	}
	return u.Scheme + "://" + u.Host + "/••••••••"
}
func (h *Handler) ExportPortable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if readJSON(r, &req) != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	data, err := h.backup.ExportPortable(req.Password)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="ddns-agent-portable.json"`)
	_, _ = w.Write(data)
}
func importBody(r *http.Request) ([]byte, string, error) {
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, (16<<20)+1))
	if err != nil || len(body) > 16<<20 {
		return nil, "", errors.New("import exceeds request limit")
	}
	var wrapper struct {
		Data     string `json:"data"`
		Password string `json:"password"`
	}
	if json.Unmarshal(body, &wrapper) != nil {
		return nil, "", errors.New("invalid import JSON")
	}
	if wrapper.Data != "" {
		return []byte(wrapper.Data), wrapper.Password, nil
	}
	return body, "", nil
}
func (h *Handler) PreviewImport(w http.ResponseWriter, r *http.Request) {
	data, password, err := importBody(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	config, err := h.backup.Prepare(data, password)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"records": len(config.Records), "new_webhooks": len(config.Webhooks), "settings": len(config.Settings), "includes_users": false})
}
func (h *Handler) BackupStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, h.backup.Status())
}
func (h *Handler) RunBackup(w http.ResponseWriter, r *http.Request) {
	if err := h.backup.RunBackup(); err != nil {
		writeError(w, 500, "backup failed")
		return
	}
	writeJSON(w, 200, h.backup.Status())
}
