package api

import (
	"net/http"

	"ddns-agent/internal/provider"
	"ddns-agent/internal/provider/constants"

	"github.com/go-chi/chi/v5"
)

func (h *Handler) ListProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, provider.AllProviderInfos())
}

func (h *Handler) ProviderFields(w http.ResponseWriter, r *http.Request) {
	name := constants.Provider(chi.URLParam(r, "name"))
	info := provider.GetProviderInfo(name)
	writeJSON(w, http.StatusOK, info)
}
