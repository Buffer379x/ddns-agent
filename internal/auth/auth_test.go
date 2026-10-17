package auth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ddns-agent/internal/database"

	"github.com/golang-jwt/jwt/v5"
)

func fixture(t *testing.T) (*Service, *database.DB, *database.User) {
	t.Helper()
	db, err := database.New(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	user, err := db.CreateUser("admin", "hash", "admin")
	if err != nil {
		t.Fatal(err)
	}
	return NewService(strings.Repeat("x", 32), db), db, user
}
func TestSessionRevocationAndLiveRoles(t *testing.T) {
	s, db, user := fixture(t)
	db.CreateUser("other-admin", "hash", "admin")
	token, _ := s.GenerateToken(user.ID, user.Username, user.Role)
	if _, err := s.ValidateToken(token); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateUser(user.ID, user.Username, "", "viewer"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ValidateToken(token); err == nil {
		t.Fatal("demotion did not revoke session")
	}
	token, _ = s.GenerateToken(user.ID, user.Username, "admin")
	claims, err := s.ValidateToken(token)
	if err != nil || claims["role"] != "viewer" {
		t.Fatalf("stale role: %v %v", claims, err)
	}
	db.UpdateUser(user.ID, user.Username, "new-hash", "viewer")
	if _, err = s.ValidateToken(token); err == nil {
		t.Fatal("password change did not revoke session")
	}
	token, _ = s.GenerateToken(user.ID, user.Username, "viewer")
	db.DeleteUser(user.ID)
	if _, err = s.ValidateToken(token); err == nil {
		t.Fatal("deleted user still authenticated")
	}
}
func TestLogoutAndJWTConstraints(t *testing.T) {
	s, _, user := fixture(t)
	token, _ := s.GenerateToken(user.ID, user.Username, user.Role)
	if err := s.Logout(token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ValidateToken(token); err == nil {
		t.Fatal("logout did not revoke")
	}
	for _, claims := range []jwt.MapClaims{{"sub": user.ID, "jti": "x"}, {"sub": user.ID, "jti": "x", "exp": time.Now().Add(-time.Second).Unix()}} {
		raw, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.secret)
		if _, err := s.ValidateToken(raw); err == nil {
			t.Fatal("accepted invalid JWT")
		}
	}
	raw, _ := jwt.NewWithClaims(jwt.SigningMethodHS384, jwt.MapClaims{"exp": time.Now().Add(time.Hour).Unix()}).SignedString(s.secret)
	if _, err := s.ValidateToken(raw); err == nil {
		t.Fatal("accepted HS384")
	}
}
func TestCookieCSRFAndForcedPasswordChange(t *testing.T) {
	s, db, user := fixture(t)
	token, _ := s.GenerateToken(user.ID, user.Username, user.Role)
	handler := s.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, tt := range []struct {
		origin string
		status int
	}{{"", 403}, {"https://evil.example", 403}, {"https://app.example", 204}} {
		r := httptest.NewRequest("POST", "https://app.example/api/records", nil)
		r.AddCookie(&http.Cookie{Name: CookieName, Value: token})
		r.Header.Set("Origin", tt.origin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tt.status {
			t.Fatalf("origin %q got %d", tt.origin, w.Code)
		}
	}
	db.RequirePasswordChange(user.ID)
	r := httptest.NewRequest("GET", "https://app.example/api/records", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("must-change bypass: %d", w.Code)
	}
}
func TestRateLimitIsBoundedAndIgnoresSpoofedForwarding(t *testing.T) {
	limiter := NewRateLimiter(2, time.Minute)
	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for i := 0; i < 3; i++ {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "192.0.2.1:1234"
		r.Header.Set("X-Forwarded-For", fmt.Sprint(i))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if i == 2 && w.Code != 429 {
			t.Fatalf("spoof bypass %d", w.Code)
		}
	}
	for i := 0; i < 5000; i++ {
		limiter.Allow(fmt.Sprint(i))
	}
	if len(limiter.visitors) > 4096 {
		t.Fatal("unbounded limiter")
	}
}
