package api

import (
	"net/http"

	"ddns-agent/internal/database"
	"ddns-agent/internal/validation"
)

func (h *Handler) ListWebhooks(w http.ResponseWriter, r *http.Request) {
	hooks, err := h.db.ListWebhooks()
	if err != nil {
		writeError(w, 500, "cannot load webhooks")
		return
	}
	if hooks == nil {
		hooks = []database.Webhook{}
	}
	for i := range hooks {
		plain, err := h.backup.OpenWebhook(hooks[i].URL)
		if err != nil {
			writeError(w, 500, "cannot decrypt webhook")
			return
		}
		hooks[i].DisplayURL = webhookDisplay(plain, hooks[i].Type)
		hooks[i].HasSecret = true
		hooks[i].URL = ""
	}
	writeJSON(w, 200, hooks)
}

func (h *Handler) CreateWebhook(w http.ResponseWriter, r *http.Request) {
	var hook database.Webhook
	if readJSON(r, &hook) != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if err := validation.Webhook(&hook); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	cipher, err := h.encryptor.Encrypt(hook.URL)
	if err != nil {
		writeError(w, 500, "encrypting webhook")
		return
	}
	hook.URL = cipher
	id, err := h.db.CreateWebhook(&hook)
	if err != nil {
		writeError(w, 500, "cannot create webhook")
		return
	}
	hook.ID = int(id)
	hook.URL = ""
	writeJSON(w, 201, hook)
}

func (h *Handler) UpdateWebhook(w http.ResponseWriter, r *http.Request) {
	var hook database.Webhook
	if readJSON(r, &hook) != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	hook.ID = paramInt(r, "id")
	old, err := h.db.GetWebhook(hook.ID)
	if err != nil {
		writeError(w, 404, "webhook not found")
		return
	}
	if hook.URL == "" {
		hook.URL, err = h.backup.OpenWebhook(old.URL)
		if err != nil {
			writeError(w, 500, "cannot decrypt webhook")
			return
		}
	}
	if err = validation.Webhook(&hook); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	hook.URL, err = h.encryptor.Encrypt(hook.URL)
	if err != nil {
		writeError(w, 500, "encrypting webhook")
		return
	}
	if err = h.db.UpdateWebhook(&hook); err != nil {
		writeError(w, 500, "cannot update webhook")
		return
	}
	hook.URL = ""
	writeJSON(w, 200, hook)
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
