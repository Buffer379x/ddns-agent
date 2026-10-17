package api

import (
	"io/fs"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
)

var localeRe = regexp.MustCompile(`^[a-z]{2}(-[A-Z]{2})?$`)

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
	// Validate locale strictly to prevent path traversal or unexpected file access.
	if !localeRe.MatchString(locale) {
		writeError(w, http.StatusBadRequest, "invalid locale")
		return
	}
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
