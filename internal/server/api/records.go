package api

import (
	"errors"
	"net/http"

	"ddns-agent/internal/database"
	"ddns-agent/internal/netutil"
	"ddns-agent/internal/validation"
)

func (h *Handler) ListRecords(w http.ResponseWriter, r *http.Request) {
	records, err := h.db.ListRecords()
	if err != nil {
		writeError(w, 500, "cannot load records")
		return
	}
	for i := range records {
		records[i].HasCredentials = records[i].ProviderConfig != ""
		records[i].ProviderConfig = ""
	}
	writeJSON(w, 200, records)
}

func (h *Handler) CreateRecord(w http.ResponseWriter, r *http.Request) {
	var rec database.Record
	if err := readJSON(r, &rec); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if err := validation.Record(&rec); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	cipher, err := h.encryptor.Encrypt(rec.ProviderConfig)
	if err != nil {
		writeError(w, 500, "encrypting configuration")
		return
	}
	rec.ProviderConfig = cipher
	err = h.updater.Exclusive(func() error { id, err := h.db.CreateRecord(&rec); rec.ID = int(id); return err })
	if err != nil {
		writeError(w, 500, "cannot create record")
		return
	}
	h.log.Success("api", "hostname created: %s.%s", rec.Owner, rec.Domain)
	rec.ProviderConfig = ""
	rec.HasCredentials = true
	writeJSON(w, 201, rec)
}

func (h *Handler) UpdateRecord(w http.ResponseWriter, r *http.Request) {
	var rec database.Record
	if err := readJSON(r, &rec); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	rec.ID = paramInt(r, "id")
	err := h.updater.Exclusive(func() error {
		old, err := h.db.GetRecord(rec.ID)
		if err != nil {
			return err
		}
		if rec.Provider != old.Provider {
			return errors.New("provider cannot be changed; create a new record")
		}
		plain, err := h.encryptor.OpenConfig(old.ProviderConfig)
		if err != nil {
			return errors.New("existing credentials cannot be decrypted")
		}
		if rec.ProviderConfig == "" {
			rec.ProviderConfig = plain
		} else {
			rec.ProviderConfig, err = validation.MergeConfig(rec.ProviderConfig, plain, rec.Provider)
			if err != nil {
				return err
			}
		}
		if err = validation.Record(&rec); err != nil {
			return err
		}
		if rec.ProviderConfig == plain {
			rec.ProviderConfig = old.ProviderConfig
		} else {
			rec.ProviderConfig, err = h.encryptor.Encrypt(rec.ProviderConfig)
		}
		if err != nil {
			return err
		}
		return h.db.UpdateRecord(&rec)
	})
	if err != nil {
		writeError(w, 400, netutil.Redact(err.Error()))
		return
	}
	rec.ProviderConfig = ""
	rec.HasCredentials = true
	writeJSON(w, 200, rec)
}

func (h *Handler) DeleteRecord(w http.ResponseWriter, r *http.Request) {
	err := h.updater.Exclusive(func() error { return h.db.DeleteRecord(paramInt(r, "id")) })
	if err != nil {
		writeError(w, 500, "cannot delete record")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

func (h *Handler) RefreshRecord(w http.ResponseWriter, r *http.Request) {
	if err := h.updater.RequestUpdate(paramInt(r, "id")); err != nil {
		writeError(w, 400, "refresh rejected: "+netutil.SafeError(err))
		return
	}
	writeJSON(w, 202, map[string]string{"status": "refresh queued"})
}

func (h *Handler) RefreshAll(w http.ResponseWriter, r *http.Request) {
	h.log.Info("updater", "manual refresh of all active records initiated")
	if !h.updater.TriggerUpdate() {
		h.log.Warn("updater", "update already pending, skipping duplicate trigger")
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "refresh initiated"})
}
