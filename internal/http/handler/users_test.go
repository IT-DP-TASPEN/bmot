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

func TestLocalAuthenticationAndUserManagement(t *testing.T) {
	accounts := testAccounts()
	renderer, err := view.New("../../../web/templates")
	if err != nil {
		t.Fatal(err)
	}
	app := &App{Service: mock.NewDashboardService(), Sessions: middleware.NewSessions(accounts), Users: accounts, View: renderer}
	serve := func(method, path string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
		var body string
		if form != nil {
			body = form.Encode()
		}
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		app.Routes().ServeHTTP(w, req)
		return w
	}
	login := func(name, password string) *httptest.ResponseRecorder {
		return serve("POST", "/login", url.Values{"username": {name}, "password": {password}}, nil)
	}
	for _, tc := range []struct{ name, password string }{{"bm001", "wrong-password"}, {"missing", "test-password"}} {
		w := login(tc.name, tc.password)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Nama pengguna atau kata sandi salah") || len(w.Result().Cookies()) != 0 {
			t.Fatalf("invalid login %s: %d", tc.name, w.Code)
		}
	}
	branch := login("bm001", "test-password")
	if branch.Code != 303 {
		t.Fatalf("valid login: %d", branch.Code)
	}
	branchCookie := branch.Result().Cookies()[0]
	if w := serve("GET", "/users", nil, branchCookie); w.Code != 403 {
		t.Fatalf("user management USER: %d", w.Code)
	}
	if w := serve("POST", "/users", url.Values{"username": {"intruder"}}, branchCookie); w.Code != 403 {
		t.Fatalf("USER write: %d", w.Code)
	}
	branchAccount, _ := accounts.ByUsername(httptest.NewRequest("GET", "/", nil).Context(), "bm001")
	branchAccount.IsActive = false
	if _, err := accounts.Save(httptest.NewRequest("GET", "/", nil).Context(), branchAccount, ""); err != nil {
		t.Fatal(err)
	}
	if w := serve("GET", "/dashboard", nil, branchCookie); w.Code != 303 {
		t.Fatalf("inactive session retained access: %d", w.Code)
	}
	if w := login("bm001", "test-password"); w.Code != 200 {
		t.Fatalf("inactive login: %d", w.Code)
	}
	admin := login("rootuser", "test-password")
	if admin.Code != 303 {
		t.Fatalf("admin login: %d", admin.Code)
	}
	adminCookie := admin.Result().Cookies()[0]
	if !adminCookie.HttpOnly || adminCookie.SameSite != http.SameSiteLaxMode || adminCookie.MaxAge <= 0 {
		t.Fatal("unsafe session cookie")
	}
	rotated := serve("POST", "/login", url.Values{"username": {"rootuser"}, "password": {"test-password"}}, adminCookie)
	if rotated.Code != 303 || rotated.Result().Cookies()[0].Value == adminCookie.Value {
		t.Fatal("session ID not rotated")
	}
	if w := serve("GET", "/users", nil, adminCookie); w.Code != 303 {
		t.Fatalf("old session retained access: %d", w.Code)
	}
	adminCookie = rotated.Result().Cookies()[0]
	list := serve("GET", "/users", nil, adminCookie)
	if list.Code != 200 || !strings.Contains(list.Body.String(), "User Management") || strings.Contains(list.Body.String(), "test-password") {
		t.Fatalf("admin list: %d", list.Code)
	}
	csrfReq := httptest.NewRequest("GET", "/users", nil)
	csrfReq.AddCookie(adminCookie)
	csrf := app.Sessions.CSRF(csrfReq)
	form := url.Values{"csrf_token": {csrf}, "username": {"0985000"}, "full_name": {"Test Employee"}, "position": {"Branch Manager"}, "branch_code": {"007"}, "role": {"USER"}, "is_active": {"on"}, "password": {"initial-password"}}
	if w := serve("POST", "/users", form, adminCookie); w.Code != 303 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if w := serve("POST", "/users", form, adminCookie); w.Code != 400 || !strings.Contains(w.Body.String(), "Username sudah digunakan") {
		t.Fatalf("duplicate: %d", w.Code)
	}
	created, _ := accounts.ByUsername(csrfReq.Context(), "0985000")
	if !created.CheckPassword("initial-password") {
		t.Fatal("create password not hashed")
	}
	path := "/users/" + "4" // IDs in testAccounts are sequential.
	form.Set("full_name", "Updated Employee")
	form.Set("password", "")
	if w := serve("POST", path, form, adminCookie); w.Code != 303 {
		t.Fatalf("edit: %d", w.Code)
	}
	edited, _ := accounts.ByUsername(csrfReq.Context(), "0985000")
	if edited.FullName != "Updated Employee" || !edited.CheckPassword("initial-password") {
		t.Fatal("metadata edit changed password")
	}
	form.Set("password", "new-password")
	if w := serve("POST", path, form, adminCookie); w.Code != 303 {
		t.Fatalf("password change: %d", w.Code)
	}
	edited, _ = accounts.ByUsername(csrfReq.Context(), "0985000")
	if !edited.CheckPassword("new-password") || edited.CheckPassword("initial-password") {
		t.Fatal("password change failed")
	}
	form.Set("password", "")
	form.Del("is_active")
	if w := serve("POST", path, form, adminCookie); w.Code != 303 {
		t.Fatalf("deactivate: %d", w.Code)
	}
	if w := login("0985000", "new-password"); w.Code != 200 {
		t.Fatalf("inactive login: %d", w.Code)
	}
	if w := serve("POST", "/users", url.Values{"username": {"no-csrf"}}, adminCookie); w.Code != 403 {
		t.Fatalf("CSRF: %d", w.Code)
	}
	if w := serve("POST", "/logout", url.Values{"csrf_token": {csrf}}, adminCookie); w.Code != 303 {
		t.Fatalf("logout: %d", w.Code)
	}
	if w := serve("GET", "/users", nil, adminCookie); w.Code != 303 {
		t.Fatalf("session survived logout: %d", w.Code)
	}
}
