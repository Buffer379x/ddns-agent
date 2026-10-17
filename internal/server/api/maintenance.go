package api

import (
	"net/http"
)

func (h *Handler) DatabaseCheck(w http.ResponseWriter, r *http.Request) {
	h.log.Info("database", "database integrity check started")
	issues, err := h.db.CheckIntegrity()
	if err != nil {
		h.log.Error("database", "integrity check error: %s", err.Error())
		writeError(w, http.StatusInternalServerError, "integrity check failed: "+err.Error())
		return
	}
	if len(issues) == 0 {
		h.log.Success("database", "integrity check passed — no issues found")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "issues": []string{}})
		return
	}
	for _, issue := range issues {
		h.log.Warn("database", "integrity issue: %s", issue)
	}
	h.log.Warn("database", "integrity check completed — %d issue(s) found", len(issues))
	writeJSON(w, http.StatusOK, map[string]any{"ok": false, "issues": issues})
}

func (h *Handler) DatabaseRepair(w http.ResponseWriter, r *http.Request) {
	h.log.Info("database", "database repair started — creating backup first")

	// --- Tier 1: clean VACUUM backup ---
	backupErr := h.backup.RunBackup()
	if backupErr != nil {
		h.log.Warn("database", "VACUUM backup failed (%s) — attempting raw file copy as fallback", backupErr.Error())

		// --- Tier 2: raw byte-copy (works even on malformed DBs) ---
		dbPath := h.db.Path()
		if dbPath == "" {
			h.log.Error("database", "database path unknown; cannot create fallback backup")
			writeError(w, http.StatusInternalServerError, "backup failed: "+backupErr.Error())
			return
		}
		dest, rawErr := h.backup.RunRawCopyBackup(dbPath)
		if rawErr != nil {
			h.log.Error("database", "raw copy backup also failed: %s", rawErr.Error())
			writeError(w, http.StatusInternalServerError,
				"backup failed (VACUUM: "+backupErr.Error()+"; raw copy: "+rawErr.Error()+")")
			return
		}
		h.log.Info("database", "raw copy backup saved to %s", dest)
	} else {
		h.log.Info("database", "VACUUM backup created successfully")
	}

	// --- Repair: re-run schema migrations ---
	h.log.Info("database", "backup ready, running schema repair")
	if err := h.db.Repair(); err != nil {
		h.log.Error("database", "repair failed: %s", err.Error())
		writeError(w, http.StatusInternalServerError, "repair failed: "+err.Error())
		return
	}

	// --- Post-repair integrity check ---
	issues, err := h.db.CheckIntegrity()
	if err != nil {
		h.log.Warn("database", "post-repair integrity check error: %s", err.Error())
		writeError(w, 500, "repair could not be verified")
		return
	} else if len(issues) == 0 {
		h.log.Success("database", "repair completed — database integrity verified")
	} else {
		for _, issue := range issues {
			h.log.Warn("database", "remaining issue after repair: %s", issue)
		}
		h.log.Warn("database", "repair completed with %d remaining issue(s)", len(issues))
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "remaining_issues": issues})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "remaining_issues": []string{}})
}

// --- SSE ---
