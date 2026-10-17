package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"ddns-agent/internal/validation"
)

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
	if readJSON(r, &settings) != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if err := validation.Settings(settings); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := h.db.SetSettings(settings); err != nil {
		writeError(w, 500, "cannot save settings")
		return
	}
	h.applySettings(settings)
	writeJSON(w, 200, map[string]string{"status": "updated"})
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

func (h *Handler) applySettings(settings map[string]string) {
	if v, ok := settings["cooldown_seconds"]; ok {
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		h.updater.SetCooldown(time.Duration(n) * time.Second)
	}
	if v, ok := settings["refresh_interval"]; ok {
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		h.updater.SetPeriod(time.Duration(n) * time.Second)
	}
	if v, ok := settings["http_timeout_seconds"]; ok {
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		h.updater.SetHTTPTimeout(time.Duration(n) * time.Second)
	}
	if v, ok := settings["backup_retention"]; ok {
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		h.backup.SetRetention(n)
	}
	if v, ok := settings["log_archive_days"]; ok {
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		h.log.SetLogRetentionDays(n)
	}
	if _, ok := settings["app_timezone"]; ok {
		h.syncAppTimezone(settings["app_timezone"])
	}
}
