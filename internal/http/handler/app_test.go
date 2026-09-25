package handler

import (
	"context"
	"database/sql"
	"github.com/ibldzn/dashboard-roro-jongrang/internal/service/users"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/ibldzn/dashboard-roro-jongrang/internal/domain"
	"github.com/ibldzn/dashboard-roro-jongrang/internal/http/middleware"
	"github.com/ibldzn/dashboard-roro-jongrang/internal/service"
	"github.com/ibldzn/dashboard-roro-jongrang/internal/service/mock"
	"github.com/ibldzn/dashboard-roro-jongrang/internal/view"
)

func TestRoutesLoginAndBranchScope(t *testing.T) {
	renderer, err := view.New("../../../web/templates")
	if err != nil {
		t.Fatal(err)
	}
	app := &App{Service: mock.NewDashboardService(), Sessions: middleware.NewSessions(testAccounts()), View: renderer}
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
	login := serve("POST", "/login", url.Values{"username": {"bm001"}, "password": {"test-password"}}.Encode(), nil)
	if login.Code != http.StatusSeeOther || len(login.Result().Cookies()) == 0 {
		t.Fatal("login failed")
	}
	cookie := login.Result().Cookies()[0]
	for _, route := range []string{"/dashboard", "/tabungan?category=abp", "/deposito?category=jatuh-tempo", "/kredit", "/kinerja", "/nominatif?domain=kredit&category=organik&metric=bade"} {
		w := serve("GET", route, "", cookie)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", route, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "001 - KPO") {
			t.Fatalf("branch scope missing on %s", route)
		}
		if route == "/kredit" && strings.Contains(w.Body.String(), "Limit") {
			t.Fatal("limit still appears on the Kredit dashboard")
		}
	}
	if w := serve("GET", "/dashboard?branch=002", "", cookie); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "001 - KPO 🔒") {
		t.Fatalf("cross branch status %d", w.Code)
	}
	csrfReq := httptest.NewRequest("GET", "/dashboard", nil)
	csrfReq.AddCookie(cookie)
	if w := serve("POST", "/logout", url.Values{"csrf_token": {app.Sessions.CSRF(csrfReq)}}.Encode(), cookie); w.Code != http.StatusSeeOther {
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
	app := &App{Service: mock.NewDashboardService(), Sessions: middleware.NewSessions(testAccounts()), View: renderer}
	login := httptest.NewRecorder()
	loginRequest := httptest.NewRequest("POST", "/login", strings.NewReader("username=rootuser&password=test-password"))
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
			expectedDate := "Posisi 24 September 2026 · 001 - KPO"
			if strings.HasPrefix(path, "/nominatif") {
				expectedDate = "Periode berakhir 24 September 2026 · 001 - KPO"
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

type branchSpy struct {
	service.DashboardService
	last  domain.Filter
	calls int
}

func (s *branchSpy) record(f domain.Filter) { s.last, s.calls = f, s.calls+1 }
func (s *branchSpy) GetOverview(ctx context.Context, f domain.Filter) (domain.Dashboard, error) {
	s.record(f)
	return s.DashboardService.GetOverview(ctx, f)
}
func (s *branchSpy) GetSavings(ctx context.Context, f domain.Filter, category string) (domain.Dashboard, error) {
	s.record(f)
	return s.DashboardService.GetSavings(ctx, f, category)
}
func (s *branchSpy) GetDeposits(ctx context.Context, f domain.Filter, category string) (domain.Dashboard, error) {
	s.record(f)
	return s.DashboardService.GetDeposits(ctx, f, category)
}
func (s *branchSpy) GetLoans(ctx context.Context, f domain.Filter) (domain.Dashboard, error) {
	s.record(f)
	return s.DashboardService.GetLoans(ctx, f)
}
func (s *branchSpy) GetFinancialPerformance(ctx context.Context, f domain.Filter) (domain.Dashboard, error) {
	s.record(f)
	return s.DashboardService.GetFinancialPerformance(ctx, f)
}
func (s *branchSpy) GetNominative(ctx context.Context, f domain.NominativeFilter) (domain.NominativeResult, error) {
	s.record(f.Filter)
	return s.DashboardService.GetNominative(ctx, f)
}

func TestPageAwareBranchScope(t *testing.T) {
	renderer, err := view.New("../../../web/templates")
	if err != nil {
		t.Fatal(err)
	}
	spy := &branchSpy{DashboardService: mock.NewDashboardService()}
	app := &App{Service: spy, Sessions: middleware.NewSessions(testAccounts()), View: renderer}
	serve := func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		app.Routes().ServeHTTP(w, req)
		return w
	}
	login := func(username string) *http.Cookie {
		password := "test-password"
		if username == "rootuser" {
			password = "test-password"
		}
		w := serve("POST", "/login", url.Values{"username": {username}, "password": {password}}.Encode(), nil)
		if w.Code != http.StatusSeeOther || len(w.Result().Cookies()) == 0 {
			t.Fatalf("login %s: %d", username, w.Code)
		}
		return w.Result().Cookies()[0]
	}
	branchUser := login("bm007")
	for _, tc := range []struct {
		name, path, want string
		picker           bool
	}{
		{"ringkasan", "/dashboard?branch=003", "007", false},
		{"tabungan", "/tabungan?branch=ALL", "007", false},
		{"deposito", "/deposito?branch=003", "007", false},
		{"kinerja branch", "/kinerja?branch=003", "003", true},
		{"kinerja konsolidasi", "/kinerja?branch=ALL", "ALL", true},
		{"kredit after kinerja", "/kredit?branch=003&mode=daily&period=2026-09-23", "007", false},
		{"nominatif", "/nominatif?domain=kredit&category=organik&metric=bade&branch=003", "007", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := serve("GET", tc.path, "", branchUser)
			if w.Code != http.StatusOK || spy.last.Branch != tc.want {
				t.Fatalf("status %d, service branch %q, body %s", w.Code, spy.last.Branch, w.Body.String())
			}
			if tc.name == "kredit after kinerja" && (spy.last.Mode != "daily" || spy.last.Period != "2026-09-23") {
				t.Fatalf("reporting context lost: %+v", spy.last)
			}
			if !strings.Contains(w.Body.String(), "value=\""+tc.want+"\"") {
				t.Fatalf("effective branch missing from UI: %s", w.Body.String())
			}
			if tc.picker && !strings.Contains(w.Body.String(), "<select name=\"branch\">") {
				t.Fatal("Kinerja branch picker missing")
			}
			if !tc.picker && strings.Contains(w.Body.String(), "<select name=\"branch\">") {
				t.Fatal("restricted branch picker exposed")
			}
			if tc.name == "kinerja branch" && (!strings.Contains(w.Body.String(), "href=\"/kredit?branch=007") || !strings.Contains(w.Body.String(), "href=\"/kinerja?branch=003")) {
				t.Fatal("navigation did not keep the destination page's branch scope")
			}
		})
	}
	admin := login("rootuser")
	for _, path := range []string{"/dashboard?branch=003", "/tabungan?branch=003", "/kredit?branch=003", "/kinerja?branch=003"} {
		w := serve("GET", path, "", admin)
		if w.Code != http.StatusOK || spy.last.Branch != "003" {
			t.Fatalf("admin %s: status %d, branch %q", path, w.Code, spy.last.Branch)
		}
	}
	for _, path := range []string{"/kinerja?branch=000", "/dashboard?branch=999", "/kredit?branch=evil"} {
		calls := spy.calls
		w := serve("GET", path, "", branchUser)
		if w.Code != http.StatusBadRequest || spy.calls != calls {
			t.Fatalf("invalid %s: status %d, calls %d -> %d", path, w.Code, calls, spy.calls)
		}
	}
}

func TestBranchLabelsRender(t *testing.T) {
	renderer, err := view.New("../../../web/templates")
	if err != nil {
		t.Fatal(err)
	}
	app := &App{Service: mock.NewDashboardService(), Sessions: middleware.NewSessions(testAccounts()), View: renderer}
	login := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/login", strings.NewReader("username=rootuser&password=test-password"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	app.Routes().ServeHTTP(login, req)
	req = httptest.NewRequest("GET", "/dashboard", nil)
	req.AddCookie(login.Result().Cookies()[0])
	w := httptest.NewRecorder()
	app.Routes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	for _, option := range []struct{ code, label string }{
		{"ALL", "Konsolidasi"},
		{"001", "001 - KPO"},
		{"002", "002 - KC Bogor"},
		{"003", "003 - KC Depok"},
		{"004", "004 - KC Tangerang"},
		{"005", "005 - KC Jaktim"},
		{"006", "006 - KC Karawang"},
		{"007", "007 - KC Cikarang"},
		{"008", "008 - KC Purwokerto"},
	} {
		if !strings.Contains(w.Body.String(), "value=\""+option.code+"\"") {
			t.Errorf("missing option %s", option.label)
		}
		if !strings.Contains(w.Body.String(), ">"+option.label+"</option>") {
			t.Errorf("missing label %s", option.label)
		}
	}
}

type fakeAccounts struct {
	mu   sync.Mutex
	byID map[int64]users.User
	next int64
}

func testAccounts() *fakeAccounts {
	f := &fakeAccounts{byID: map[int64]users.User{}}
	for _, u := range []users.User{
		{Username: "rootuser", FullName: "Administrator", Position: "Administrator", Role: "ADMIN", IsActive: true},
		{Username: "bm001", FullName: "BM KPO", Position: "Branch Manager", Role: "USER", BranchCode: "001", IsActive: true},
		{Username: "bm007", FullName: "BM Cikarang", Position: "Branch Manager", Role: "USER", BranchCode: "007", IsActive: true},
	} {
		_, _ = f.Save(context.Background(), u, "test-password")
	}
	return f
}
func (f *fakeAccounts) ByUsername(_ context.Context, name string) (users.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.byID {
		if u.Username == name {
			return u, nil
		}
	}
	return users.User{}, sql.ErrNoRows
}
func (f *fakeAccounts) ByID(_ context.Context, id int64) (users.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.byID[id]
	if !ok {
		return u, sql.ErrNoRows
	}
	return u, nil
}
func (f *fakeAccounts) List(_ context.Context) ([]users.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []users.User
	for _, u := range f.byID {
		out = append(out, u)
	}
	return out, nil
}
func (f *fakeAccounts) Save(_ context.Context, u users.User, password string) (int64, error) {
	if err := users.Validate(u, password, u.ID == 0); err != nil {
		return 0, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, other := range f.byID {
		if other.Username == u.Username && id != u.ID {
			return 0, users.ErrDuplicate
		}
	}
	if u.ID == 0 {
		f.next++
		u.ID = f.next
	} else if _, ok := f.byID[u.ID]; !ok {
		return 0, sql.ErrNoRows
	}
	if password != "" {
		if err := u.SetPassword(password); err != nil {
			return 0, err
		}
	} else {
		old := f.byID[u.ID]
		u.KeepPassword(old)
	}
	f.byID[u.ID] = u
	return u.ID, nil
}
