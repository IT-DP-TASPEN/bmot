package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ibldzn/dashboard-roro-jongrang/internal/http/middleware"
	"github.com/ibldzn/dashboard-roro-jongrang/internal/service/mock"
	"github.com/ibldzn/dashboard-roro-jongrang/internal/view"
)

func TestRoutesLoginAndBranchScope(t *testing.T) {
	renderer, err := view.New("../../../web/templates")
	if err != nil {
		t.Fatal(err)
	}
	app := &App{Service: mock.NewDashboardService(), Sessions: middleware.NewSessions(), View: renderer}
	serve := func(method, target, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		if cookie != nil {
			req.AddCookie(cookie)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		w := httptest.NewRecorder()
		app.Routes().ServeHTTP(w, req)
		return w
	}
	if w := serve("GET", "/dashboard", "", nil); w.Code != http.StatusSeeOther {
		t.Fatalf("unauthenticated status %d", w.Code)
	}
	login := serve("POST", "/login", url.Values{"username": {"branch001"}, "password": {"demo"}}.Encode(), nil)
	if login.Code != http.StatusSeeOther || len(login.Result().Cookies()) == 0 {
		t.Fatal("login failed")
	}
	cookie := login.Result().Cookies()[0]
	for _, route := range []string{"/dashboard", "/tabungan?category=abp", "/deposito?category=jatuh-tempo", "/kredit", "/kinerja", "/nominatif?domain=kredit&category=organik&metric=bade"} {
		w := serve("GET", route, "", cookie)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", route, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "Cabang 001") && !strings.Contains(w.Body.String(), "001 🔒") {
			t.Fatalf("branch scope missing on %s", route)
		}
	}
	if w := serve("GET", "/dashboard?branch=002", "", cookie); w.Code != http.StatusForbidden {
		t.Fatalf("cross branch status %d", w.Code)
	}
	if w := serve("POST", "/logout", "", cookie); w.Code != http.StatusSeeOther {
		t.Fatalf("logout status %d", w.Code)
	}
	if w := serve("GET", "/dashboard", "", cookie); w.Code != http.StatusSeeOther {
		t.Fatalf("session survived logout: %d", w.Code)
	}
}
