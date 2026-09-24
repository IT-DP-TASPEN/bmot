package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"

	"github.com/ibldzn/dashboard-roro-jongrang/internal/domain"
)

type User struct{ Name, Branch string }

type Sessions struct {
	mu     sync.RWMutex
	tokens map[string]User
}

func NewSessions() *Sessions { return &Sessions{tokens: make(map[string]User)} }

func (s *Sessions) Login(w http.ResponseWriter, r *http.Request, username, password string) bool {
	var u User
	switch {
	case username == "admin" && password == "admin":
		u = User{Name: "admin", Branch: "ALL"}
	case strings.HasPrefix(username, "branch") && password == "demo":
		branch := strings.TrimPrefix(username, "branch")
		if branch == "ALL" {
			return false
		}
		if _, ok := domain.BranchLabel(branch); !ok {
			return false
		}
		u = User{Name: username, Branch: branch}
	default:
		return false
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return false
	}
	token := hex.EncodeToString(b)
	s.mu.Lock()
	s.tokens[token] = u
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "dashboard_session", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil})
	return true
}

func (s *Sessions) User(r *http.Request) (User, bool) {
	c, err := r.Cookie("dashboard_session")
	if err != nil {
		return User{}, false
	}
	s.mu.RLock()
	u, ok := s.tokens[c.Value]
	s.mu.RUnlock()
	return u, ok
}

func (s *Sessions) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("dashboard_session"); err == nil {
		s.mu.Lock()
		delete(s.tokens, c.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "dashboard_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil})
}

func (s *Sessions) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.User(r); !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}
