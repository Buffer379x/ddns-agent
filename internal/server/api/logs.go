package api

import (
	"net/http"
	"strings"

	"ddns-agent/internal/database"
)

func (h *Handler) ListLogs(w http.ResponseWriter, r *http.Request) {
	level := strings.ToUpper(r.URL.Query().Get("level"))
	limit := queryInt(r, "limit", 100)
	offset := queryInt(r, "offset", 0)
	logs, total, err := h.db.ListLogs(level, limit, offset, r.URL.Query().Get("search"))
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
