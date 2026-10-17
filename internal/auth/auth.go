package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"ddns-agent/internal/crypto"
	"ddns-agent/internal/database"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

type contextKey string

const UserContextKey contextKey = "user"
const CookieName = "ddns_session"

type Service struct {
	secret []byte
	db     *database.DB
}

func NewService(secret string, db *database.DB) *Service {
	return &Service{secret: []byte(secret), db: db}
}
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
func ValidatePassword(password string) error {
	if len(password) < 12 || len(password) > 72 {
		return errors.New("password must contain 12–72 bytes")
	}
	return nil
}
func (s *Service) GenerateToken(userID int, username, role string) (string, error) {
	id := crypto.GenerateKey()
	expires := time.Now().Add(24 * time.Hour)
	claims := jwt.MapClaims{"sub": userID, "username": username, "role": role, "jti": id, "exp": expires.Unix(), "iat": time.Now().Unix()}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.secret)
	if err != nil {
		return "", err
	}
	if err = s.db.SaveSession(id, userID, expires); err != nil {
		return "", err
	}
	return token, nil
}
func (s *Service) ValidateToken(raw string) (jwt.MapClaims, error) {
	if raw == "" || len(raw) > 8192 {
		return nil, errors.New("invalid token")
	}
	token, err := jwt.Parse(raw, func(t *jwt.Token) (any, error) { return s.secret, nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if err != nil || !token.Valid {
		return nil, errors.New("invalid token")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("invalid claims")
	}
	id, _ := claims["jti"].(string)
	user, err := s.db.SessionUser(id)
	if err != nil {
		return nil, errors.New("session revoked")
	}
	sub, _ := claims["sub"].(float64)
	if user.ID != int(sub) {
		return nil, errors.New("invalid subject")
	}
	claims["username"] = user.Username
	claims["role"] = user.Role
	claims["must_change_password"] = user.MustChangePassword
	return claims, nil
}
func RequestToken(r *http.Request) string {
	if header := r.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer ") {
		return strings.TrimPrefix(header, "Bearer ")
	}
	if c, err := r.Cookie(CookieName); err == nil {
		return c.Value
	}
	return ""
}
func SetCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: token, Path: "/", HttpOnly: true, Secure: r.TLS != nil || r.URL.Scheme == "https", SameSite: http.SameSiteStrictMode, MaxAge: 86400})
}
func ClearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", HttpOnly: true, Secure: r.TLS != nil || r.URL.Scheme == "https", SameSite: http.SameSiteStrictMode, MaxAge: -1})
}
func (s *Service) Logout(raw string) error {
	claims, err := s.ValidateToken(raw)
	if err != nil {
		return nil
	}
	id, _ := claims["jti"].(string)
	return s.db.RevokeSession(id)
}
func (s *Service) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" && r.Method != "GET" && r.Method != "HEAD" {
			origin, err := url.Parse(r.Header.Get("Origin"))
			if err != nil || origin.Host != r.Host || (origin.Scheme != "http" && origin.Scheme != "https") {
				writeJSONError(w, 403, "same-origin request required")
				return
			}
		}
		claims, err := s.ValidateToken(RequestToken(r))
		if err != nil {
			writeJSONError(w, 401, "session expired")
			return
		}
		must, _ := claims["must_change_password"].(bool)
		if must && r.URL.Path != "/api/auth/me" && r.URL.Path != "/api/auth/password" && r.URL.Path != "/api/auth/logout" {
			writeJSONError(w, 403, "password change required")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), UserContextKey, claims)))
	})
}
func GetUserFromContext(r *http.Request) jwt.MapClaims {
	claims, _ := r.Context().Value(UserContextKey).(jwt.MapClaims)
	return claims
}
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

type visitor struct {
	count   int
	resetAt time.Time
}
type RateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	rate     int
	window   time.Duration
}

func NewRateLimiter(rate int, window time.Duration) *RateLimiter {
	return &RateLimiter{visitors: map[string]*visitor{}, rate: rate, window: window}
}
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	if v := rl.visitors[key]; v != nil && now.Before(v.resetAt) {
		v.count++
		return v.count <= rl.rate
	}
	if len(rl.visitors) >= 4096 {
		for k, v := range rl.visitors {
			if !now.Before(v.resetAt) {
				delete(rl.visitors, k)
			}
		}
		if len(rl.visitors) >= 4096 {
			return false
		}
	}
	rl.visitors[key] = &visitor{count: 1, resetAt: now.Add(rl.window)}
	return true
}
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
		if !rl.Allow(ip) {
			w.Header().Set("Retry-After", fmt.Sprint(int(rl.window.Seconds())))
			writeJSONError(w, 429, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}
