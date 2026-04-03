package middleware

import (
	"encoding/json"
	"net/http"

	"ddns-agent/internal/auth"
)

// AdminOnly rejects requests whose JWT claims do not carry the "admin" role.
// It must be placed after auth.Service.AuthMiddleware in the middleware chain.
func AdminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := auth.GetUserFromContext(r)
		if claims == nil {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		role, _ := claims["role"].(string)
		if role != "admin" {
			writeJSONError(w, http.StatusForbidden, "admin access required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequestIsAdmin reports whether the authenticated request carries the "admin" role.
func RequestIsAdmin(r *http.Request) bool {
	claims := auth.GetUserFromContext(r)
	if claims == nil {
		return false
	}
	role, _ := claims["role"].(string)
	return role == "admin"
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
