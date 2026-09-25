package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ibldzn/dashboard-roro-jongrang/internal/service/users"
	"golang.org/x/crypto/bcrypt"
)

type Accounts interface {
	ByUsername(context.Context, string) (users.User, error)
	ByID(context.Context, int64) (users.User, error)
}
type User struct {
	ID                           int64
	Name, Position, Role, Branch string
}
type session struct {
	userID  int64
	csrf    string
	expires time.Time
}
type Sessions struct {
	mu       sync.RWMutex
	tokens   map[string]session
	accounts Accounts
}

func NewSessions(accounts Accounts) *Sessions {
	return &Sessions{tokens: make(map[string]session), accounts: accounts}
}
func random() (string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return hex.EncodeToString(b), nil
}
func secure(r *http.Request) bool {
	return r.TLS != nil || os.Getenv("BM_COOKIE_SECURE") == "true"
}
func cookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: "dashboard_session", Value: value, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secure(r), MaxAge: maxAge})
}

var unknownHash, _ = bcrypt.GenerateFromPassword([]byte("unusable login sentinel"), bcrypt.DefaultCost)

func (s *Sessions) Login(w http.ResponseWriter, r *http.Request, username, password string) bool {
	u, e := s.accounts.ByUsername(r.Context(), strings.TrimSpace(username))
	if e != nil {
		_ = bcrypt.CompareHashAndPassword(unknownHash, []byte(password))
		return false
	}
	if !u.CheckPassword(password) || !u.IsActive {
		return false
	}
	token, e := random()
	if e != nil {
		return false
	}
	csrf, e := random()
	if e != nil {
		return false
	}
	s.mu.Lock()
	now := time.Now()
	for key, v := range s.tokens {
		if now.After(v.expires) {
			delete(s.tokens, key)
		}
	}
	if old, e := r.Cookie("dashboard_session"); e == nil {
		delete(s.tokens, old.Value)
	}
	s.tokens[token] = session{userID: u.ID, csrf: csrf, expires: now.Add(8 * time.Hour)}
	s.mu.Unlock()
	cookie(w, r, token, 8*3600)
	return true
}
func (s *Sessions) current(r *http.Request) (session, bool) {
	c, e := r.Cookie("dashboard_session")
	if e != nil {
		return session{}, false
	}
	s.mu.RLock()
	v, ok := s.tokens[c.Value]
	s.mu.RUnlock()
	if !ok || time.Now().After(v.expires) {
		return session{}, false
	}
	return v, true
}
func (s *Sessions) User(r *http.Request) (User, bool) {
	v, ok := s.current(r)
	if !ok {
		return User{}, false
	}
	u, e := s.accounts.ByID(r.Context(), v.userID)
	if e != nil || !u.IsActive {
		return User{}, false
	}
	return User{ID: u.ID, Name: u.FullName, Position: u.Position, Role: u.Role, Branch: u.Branch()}, true
}
func (s *Sessions) CSRF(r *http.Request) string { v, _ := s.current(r); return v.csrf }
func (s *Sessions) CheckCSRF(r *http.Request) bool {
	v, ok := s.current(r)
	return ok && v.csrf != "" && r.PostFormValue("csrf_token") == v.csrf
}
func (s *Sessions) Logout(w http.ResponseWriter, r *http.Request) {
	if c, e := r.Cookie("dashboard_session"); e == nil {
		s.mu.Lock()
		delete(s.tokens, c.Value)
		s.mu.Unlock()
	}
	cookie(w, r, "", -1)
}
func (s *Sessions) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.User(r); !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Sessions) RequireAdmin(next http.Handler) http.Handler {
	return s.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := s.User(r)
		if u.Role != "ADMIN" {
			http.Error(w, "Akses ditolak", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}))
}
