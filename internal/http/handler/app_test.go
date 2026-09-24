package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ibldzn/dashboard-roro-jongrang/internal/domain"
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
		if route == "/kredit" && strings.Contains(w.Body.String(), "Limit") {
			t.Fatal("limit still appears on the Kredit dashboard")
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

func TestFinancialChartCaptions(t *testing.T) {
	series := []domain.Series{
		{Name: "Aset", Points: []domain.Point{{Label: "23 Sep", Value: 100_000_000}, {Label: "24 Sep", Value: 100_200_000}}},
		{Name: "Laba", Points: []domain.Point{{Label: "23 Sep", Value: 5_000_000}, {Label: "24 Sep", Value: 5_100_000}}},
	}
	got := charts(series, "kinerja", "daily")
	if len(got) != 2 || got[0].Caption != "Posisi terakhir Rp 100,2 Jt · 2 hari terakhir" || got[1].Caption != "Periode terakhir Rp 5,10 Jt · 2 hari terakhir" {
		t.Fatalf("financial captions: %+v", got)
	}
	if got := charts(series, "kinerja", "yearly"); !strings.Contains(got[0].Caption, "2 tahun terakhir") {
		t.Fatalf("yearly caption: %+v", got)
	}
}

func TestReportingContextRendersInContent(t *testing.T) {
	renderer, err := view.New("../../../web/templates")
	if err != nil {
		t.Fatal(err)
	}
	app := &App{Service: mock.NewDashboardService(), Sessions: middleware.NewSessions(), View: renderer}
	login := httptest.NewRecorder()
	loginRequest := httptest.NewRequest("POST", "/login", strings.NewReader("username=admin&password=admin"))
	loginRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	app.Routes().ServeHTTP(login, loginRequest)
	cookie := login.Result().Cookies()[0]
	for _, path := range []string{"/dashboard", "/tabungan", "/deposito", "/kredit", "/kinerja", "/nominatif?domain=kredit&category=organik&metric=booking"} {
		for _, partial := range []bool{false, true} {
			separator := "?"
			if strings.Contains(path, "?") {
				separator = "&"
			}
			req := httptest.NewRequest("GET", path+separator+"mode=monthly&period=2026-09&branch=001", nil)
			req.AddCookie(cookie)
			if partial {
				req.Header.Set("HX-Request", "true")
			}
			w := httptest.NewRecorder()
			app.Routes().ServeHTTP(w, req)
			body := w.Body.String()
			expectedDate := "Posisi 24 September 2026 · Cabang 001"
			if strings.HasPrefix(path, "/nominatif") {
				expectedDate = "Periode berakhir 24 September 2026 · Cabang 001"
			}
			if w.Code != http.StatusOK || !strings.Contains(body, expectedDate) || !strings.Contains(body, "aria-label=\"Konteks pelaporan\"") || !strings.Contains(body, "value=\"monthly\" selected") || !strings.Contains(body, "value=\"001\" selected") {
				t.Fatalf("context missing on %s (partial=%t): %d %s", path, partial, w.Code, body)
			}
			if !partial {
				header := strings.SplitN(body, "</header>", 2)[0]
				if strings.Contains(header, "global-filter") || strings.Contains(header, "Posisi 24 September") {
					t.Fatalf("reporting context leaked into topbar on %s", path)
				}
			}
		}
	}
}
