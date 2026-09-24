package handler

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ibldzn/dashboard-roro-jongrang/internal/domain"
	"github.com/ibldzn/dashboard-roro-jongrang/internal/http/middleware"
	"github.com/ibldzn/dashboard-roro-jongrang/internal/service"
	"github.com/ibldzn/dashboard-roro-jongrang/internal/view"
)

type App struct {
	Service    service.DashboardService
	Sessions   *middleware.Sessions
	View       *view.Renderer
	LatestDate time.Time
	Latest     func() time.Time
	Demo       bool
	Refresh    func() bool
}

type Chart struct{ Title, Series, Type, Caption string }
type PageData struct {
	Page, Active, Path, Category, Error, Query, Back, NFDomain, NFCategory, NFMetric, NFBucket string
	User                                                                                       middleware.User
	Filter                                                                                     domain.Filter
	Dashboard                                                                                  domain.Dashboard
	Nominative                                                                                 domain.NominativeResult
	Charts                                                                                     []Chart
	Demo                                                                                       bool
	MaxYear                                                                                    int
	Provenance                                                                                 domain.Provenance
	RefreshAllowed                                                                             bool
	RefreshMessage                                                                             string
}

func (a *App) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))
	mux.HandleFunc("GET /login", a.loginPage)
	mux.HandleFunc("POST /login", a.login)
	mux.HandleFunc("POST /logout", a.logout)
	mux.Handle("GET /{$}", a.Sessions.Require(http.HandlerFunc(a.index)))
	mux.Handle("GET /dashboard", a.Sessions.Require(http.HandlerFunc(a.dashboard)))
	mux.Handle("GET /tabungan", a.Sessions.Require(http.HandlerFunc(a.savings)))
	mux.Handle("GET /deposito", a.Sessions.Require(http.HandlerFunc(a.deposits)))
	mux.Handle("GET /kredit", a.Sessions.Require(http.HandlerFunc(a.loans)))
	mux.Handle("GET /kinerja", a.Sessions.Require(http.HandlerFunc(a.financial)))
	mux.Handle("GET /nominatif", a.Sessions.Require(http.HandlerFunc(a.nominative)))
	mux.Handle("POST /refresh", a.Sessions.Require(http.HandlerFunc(a.refresh)))
	return mux
}

func (a *App) render(w http.ResponseWriter, r *http.Request, d PageData) {
	partial := r.Header.Get("HX-Request") == "true" && d.Page != "login"
	var b strings.Builder
	if err := a.View.Render(&b, d.Page, d, partial); err != nil {
		log.Printf("render %s: %v", d.Page, err)
		http.Error(w, "Gagal menampilkan halaman", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

func (a *App) loginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.Sessions.User(r); ok {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	a.render(w, r, PageData{Page: "login"})
}
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Permintaan tidak valid", 400)
		return
	}
	if !a.Sessions.Login(w, r, r.FormValue("username"), r.FormValue("password")) {
		a.render(w, r, PageData{Page: "login", Error: "Nama pengguna atau kata sandi salah."})
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	a.Sessions.Logout(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
func (a *App) index(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (a *App) base(w http.ResponseWriter, r *http.Request, active string) (PageData, bool) {
	u, _ := a.Sessions.User(r)
	latest := a.LatestDate
	if a.Latest != nil {
		latest = a.Latest()
	}
	if latest.IsZero() {
		latest = domain.LastMockDate
	}
	f, err := domain.ParseFilterAt(r.URL.Query().Get("mode"), r.URL.Query().Get("period"), r.URL.Query().Get("branch"), latest)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return PageData{}, false
	}
	if u.Branch != "ALL" && active != "kinerja" {
		f.Branch = u.Branch
	}
	d := PageData{Page: "dashboard", Active: active, Path: r.URL.Path, User: u, Filter: f, Demo: a.Demo || a.LatestDate.IsZero(), MaxYear: latest.Year(), RefreshAllowed: a.Refresh != nil && r.URL.Path != "/nominatif"}
	if source, ok := a.Service.(interface {
		Provenance(context.Context, domain.Filter) (domain.Provenance, error)
	}); ok {
		p, e := source.Provenance(r.Context(), f)
		if e == nil {
			d.Provenance = p
		}
	}
	switch r.URL.Query().Get("refresh") {
	case "started":
		d.RefreshMessage = "Pembaruan data dimulai."
	case "running":
		d.RefreshMessage = "Pembaruan data sedang berjalan."
	}
	return d, true
}

func (a *App) refresh(w http.ResponseWriter, r *http.Request) {
	if a.Refresh == nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Permintaan tidak valid", 400)
		return
	}
	path := r.FormValue("return_to")
	switch path {
	case "/dashboard", "/tabungan", "/deposito", "/kredit", "/kinerja", "/nominatif":
	default:
		path = "/dashboard"
	}
	latest := a.LatestDate
	if a.Latest != nil {
		latest = a.Latest()
	}
	if latest.IsZero() {
		latest = domain.LastMockDate
	}
	f, err := domain.ParseFilterAt(r.FormValue("mode"), r.FormValue("period"), r.FormValue("branch"), latest)
	if err != nil {
		http.Error(w, "Konteks pelaporan tidak valid", 400)
		return
	}
	status := "running"
	if a.Refresh() {
		status = "started"
	}
	http.Redirect(w, r, Link(path, f, map[string]string{"refresh": status, "category": r.FormValue("category")}), http.StatusSeeOther)
}

func (a *App) finish(w http.ResponseWriter, r *http.Request, d PageData, dashboard domain.Dashboard, err error) {
	if err != nil {
		d.Error = "Data tidak dapat dimuat: " + err.Error()
	} else {
		d.Dashboard = dashboard
		d.Charts = charts(dashboard.Series, d.Active, d.Filter.Mode)
	}
	a.render(w, r, d)
}
func charts(s []domain.Series, page, mode string) []Chart {
	makeChart := func(title string, series []domain.Series) Chart {
		b, _ := json.Marshal(series)
		unit := "bulan"
		switch mode {
		case "daily":
			unit = "hari"
		case "yearly":
			unit = "tahun"
		}
		return Chart{Title: title, Series: string(b), Type: "line", Caption: strconv.Itoa(len(series[0].Points)) + " " + unit + " terakhir"}
	}
	switch page {
	case "dashboard":
		if len(s) >= 4 {
			return []Chart{makeChart("Tren Tabungan & Deposito", s[:2]), makeChart("Tren Outstanding Kredit", s[2:4])}
		}
	case "kredit":
		if len(s) >= 4 {
			return []Chart{makeChart("Tren Booking", []domain.Series{s[0], s[2]}), makeChart("Tren BADE", []domain.Series{s[1], s[3]})}
		}
	case "deposito":
		if len(s) > 0 {
			c := makeChart("Jadwal Nominal Jatuh Tempo", s)
			c.Type = "bar"
			c.Caption = "12 minggu ke depan"
			return []Chart{c}
		}
	case "kinerja":
		if len(s) >= 2 {
			assets, profit := makeChart("Tren Aset", s[:1]), makeChart("Tren Laba", s[1:2])
			if n := len(s[0].Points); n > 0 {
				assets.Caption = "Posisi terakhir " + view.Rupiah(s[0].Points[n-1].Value) + " · " + assets.Caption
			}
			if n := len(s[1].Points); n > 0 {
				profit.Caption = "Periode terakhir " + view.Rupiah(s[1].Points[n-1].Value) + " · " + profit.Caption
			}
			return []Chart{assets, profit}
		}
	default:
		if len(s) > 0 {
			return []Chart{makeChart("Tren Saldo", s)}
		}
	}
	return nil
}

func (a *App) dashboard(w http.ResponseWriter, r *http.Request) {
	d, ok := a.base(w, r, "dashboard")
	if !ok {
		return
	}
	x, e := a.Service.GetOverview(r.Context(), d.Filter)
	a.finish(w, r, d, x, e)
}
func (a *App) savings(w http.ResponseWriter, r *http.Request) {
	d, ok := a.base(w, r, "tabungan")
	if !ok {
		return
	}
	d.Category = r.URL.Query().Get("category")
	if d.Category == "" {
		d.Category = "dpk"
	}
	x, e := a.Service.GetSavings(r.Context(), d.Filter, d.Category)
	a.finish(w, r, d, x, e)
}
func (a *App) deposits(w http.ResponseWriter, r *http.Request) {
	d, ok := a.base(w, r, "deposito")
	if !ok {
		return
	}
	d.Category = r.URL.Query().Get("category")
	if d.Category == "" {
		d.Category = "abp"
	}
	if d.Category == "jatuh-tempo" {
		x, e := a.Service.GetDepositMaturities(r.Context(), d.Filter)
		a.finish(w, r, d, x, e)
		return
	}
	x, e := a.Service.GetDeposits(r.Context(), d.Filter, d.Category)
	a.finish(w, r, d, x, e)
}
func (a *App) loans(w http.ResponseWriter, r *http.Request) {
	d, ok := a.base(w, r, "kredit")
	if !ok {
		return
	}
	x, e := a.Service.GetLoans(r.Context(), d.Filter)
	a.finish(w, r, d, x, e)
}
func (a *App) financial(w http.ResponseWriter, r *http.Request) {
	d, ok := a.base(w, r, "kinerja")
	if !ok {
		return
	}
	x, e := a.Service.GetFinancialPerformance(r.Context(), d.Filter)
	a.finish(w, r, d, x, e)
}

func (a *App) nominative(w http.ResponseWriter, r *http.Request) {
	d, ok := a.base(w, r, "nominatif")
	if !ok {
		return
	}
	d.Page = "nominative"
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	nf := domain.NominativeFilter{Filter: d.Filter, Domain: q.Get("domain"), Category: q.Get("category"), Metric: q.Get("metric"), Bucket: q.Get("bucket"), Search: q.Get("search"), Page: page}
	x, err := a.Service.GetNominative(r.Context(), nf)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	d.Nominative = x
	d.Query = nf.Search
	d.NFDomain = nf.Domain
	d.NFCategory = nf.Category
	d.NFMetric = nf.Metric
	d.NFBucket = nf.Bucket
	d.Active = nf.Domain
	d.Back = "/dashboard"
	switch nf.Domain {
	case "tabungan":
		d.Back = "/tabungan"
	case "deposito":
		d.Back = "/deposito"
	case "kredit":
		d.Back = "/kredit"
	}
	if nf.Category == "all" {
		d.Back = "/dashboard"
	}
	if d.Back != "/dashboard" && (nf.Domain == "tabungan" || nf.Domain == "deposito") {
		d.Back = Link(d.Back, d.Filter, map[string]string{"category": nf.Category})
	} else {
		d.Back = Link(d.Back, d.Filter, nil)
	}
	a.render(w, r, d)
}

func filterQuery(f domain.Filter) url.Values {
	return url.Values{"mode": {f.Mode}, "period": {f.Period}, "branch": {f.Branch}}
}

func Link(path string, f domain.Filter, extra map[string]string) string {
	q := filterQuery(f)
	for k, v := range extra {
		if v != "" {
			q.Set(k, v)
		}
	}
	return path + "?" + q.Encode()
}
