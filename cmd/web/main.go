package main

import (
	"bufio"
	"context"
	"database/sql"
	"flag"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ibldzn/dashboard-roro-jongrang/internal/http/handler"
	"github.com/ibldzn/dashboard-roro-jongrang/internal/http/middleware"
	"github.com/ibldzn/dashboard-roro-jongrang/internal/service"
	"github.com/ibldzn/dashboard-roro-jongrang/internal/service/dwh"
	"github.com/ibldzn/dashboard-roro-jongrang/internal/service/mock"
	"github.com/ibldzn/dashboard-roro-jongrang/internal/service/newsinergi"
	"github.com/ibldzn/dashboard-roro-jongrang/internal/service/realtime"
	"github.com/ibldzn/dashboard-roro-jongrang/internal/service/users"
	"github.com/ibldzn/dashboard-roro-jongrang/internal/view"
)

func loadEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok && os.Getenv(strings.TrimSpace(k)) == "" {
			_ = os.Setenv(strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), "\"'"))
		}
	}
}

func main() {
	loadEnv(".env")
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "materialize":
			materializeCommand(os.Args[2:])
			return
		case "seed-users", "bootstrap-admin":
			userCommand(os.Args[1], os.Args[2:])
			return
		}
	}
	accountStore, err := users.Open(context.Background(), os.Getenv("APP_DBSTRING"))
	if err != nil {
		log.Fatal(err)
	}
	defer accountStore.DB.Close()
	source := os.Getenv("DASHBOARD_DATA_SOURCE")
	if source == "" {
		source = "mock"
	}
	var dashboard service.DashboardService
	var latestDate time.Time
	var closeDWH func() error
	var closeNewsinergi func() error
	var store *realtime.Store
	var refresh *realtime.Refresher
	switch source {
	case "mock":
		dashboard = mock.NewDashboardService()
	case "dwh", "hybrid":
		repo, latest, err := dwh.Open(context.Background(), os.Getenv("DWH_DBSTRING"))
		if err != nil {
			log.Fatal("DWH unavailable; check DWH_DBSTRING and read-only connectivity")
		}
		closeDWH = repo.Close
		channeling, err := newsinergi.Open(context.Background(), os.Getenv("DWH_DBSTRING"))
		if err != nil {
			log.Fatal("Newsinergi unavailable; check DWH_DBSTRING and read-only connectivity")
		}
		closeNewsinergi = channeling.Close
		store, err = realtime.Open(context.Background(), os.Getenv("APP_DBSTRING"))
		if err != nil {
			log.Fatalf("application metric database unavailable: %v", err)
		}
		metrics := dwh.NewMetricStore(store.DB())
		if source == "dwh" {
			s := dwh.NewDashboardService(repo, latest)
			s.SetChanneling(channeling)
			s.SetMetricStore(metrics)
			go scheduleMaterialization(repo, channeling, metrics, s)
			dashboard, latestDate = s, latest
			break
		}
		staleAfter := duration("REALTIME_STALE_AFTER", 2*time.Hour)
		retention := integer("REALTIME_SNAPSHOT_RETENTION", 3)
		client := realtime.NewFincloud(os.Getenv("FINCLOUD_BASE_URL"), os.Getenv("FINCLOUD_USERNAME"), os.Getenv("FINCLOUD_PASSWORD"), os.Getenv("FINCLOUD_ROLE_ID"), os.Getenv("FINCLOUD_LOCATION_ID"), os.Getenv("FINCLOUD_INSECURE_TLS") == "true")
		refresh = &realtime.Refresher{Store: store, Fetcher: client, Retention: retention, StaleAfter: staleAfter}
		snapshot := func(ctx context.Context) (time.Time, time.Time, bool, error) {
			p, e := store.Latest(ctx)
			if e != nil {
				return time.Time{}, time.Time{}, false, e
			}
			if p == nil {
				return time.Time{}, time.Time{}, false, nil
			}
			return p.Date, p.PublishedAt, true, nil
		}
		hybrid := dwh.NewHybridDashboardService(repo, dwh.NewSnapshotRepository(store.DB()), latest, snapshot, staleAfter)
		hybrid.SetChanneling(channeling)
		hybrid.SetMetricStore(metrics)
		store.OnPublish = func(ctx context.Context, tx *sql.Tx, id int64, day time.Time) error {
			return metrics.MaterializeRealtime(ctx, tx, id, day, repo, channeling)
		}
		dashboard = hybrid
		latestDate = realtime.Today()
		go scheduleMaterialization(repo, channeling, metrics, hybrid)
		if os.Getenv("REALTIME_REFRESH_ENABLED") != "false" {
			go refresh.Schedule(context.Background(), duration("REALTIME_REFRESH_INTERVAL", 30*time.Minute))
		}
	default:
		log.Fatalf("unsupported DASHBOARD_DATA_SOURCE %q", source)
	}
	if closeDWH != nil {
		defer closeDWH()
	}
	if closeNewsinergi != nil {
		defer closeNewsinergi()
	}
	if store != nil {
		defer store.Close()
	}
	renderer, err := view.New("web/templates")
	if err != nil {
		log.Fatal(err)
	}
	app := &handler.App{Service: dashboard, Users: accountStore, Sessions: middleware.NewSessions(accountStore), View: renderer, LatestDate: latestDate, Demo: source == "mock"}
	if refresh != nil {
		app.Refresh = refresh.Trigger
		app.Latest = realtime.Today
	}
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("dashboard listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, app.Routes()))
}

func duration(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		log.Fatalf("invalid %s", key)
	}
	return d
}

func integer(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 {
		log.Fatalf("invalid %s", key)
	}
	return n
}

func materializeCommand(args []string) {
	fs := flag.NewFlagSet("materialize", flag.ExitOnError)
	fromArg := fs.String("from", os.Getenv("MATERIALIZE_BACKFILL_FROM"), "first date (YYYY-MM-DD)")
	toArg := fs.String("to", "", "last date (YYYY-MM-DD; default DWH watermark)")
	force := fs.Bool("force", false, "rebuild existing aggregates")
	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}
	from, err := time.Parse("2006-01-02", *fromArg)
	if err != nil {
		log.Fatal("--from or MATERIALIZE_BACKFILL_FROM is required")
	}
	ctx := context.Background()
	repo, latest, err := dwh.Open(ctx, os.Getenv("DWH_DBSTRING"))
	if err != nil {
		log.Fatal(err)
	}
	defer repo.Close()
	to := latest
	if *toArg != "" {
		to, err = time.Parse("2006-01-02", *toArg)
		if err != nil {
			log.Fatal("invalid --to")
		}
	}
	if to.After(latest) {
		log.Fatal("--to exceeds DWH watermark")
	}
	channel, err := newsinergi.Open(ctx, os.Getenv("DWH_DBSTRING"))
	if err != nil {
		log.Fatal(err)
	}
	defer channel.Close()
	store, err := realtime.Open(ctx, os.Getenv("APP_DBSTRING"))
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	if err := dwh.NewMetricStore(store.DB()).Materialize(ctx, repo, channel, from, to, *force); err != nil {
		log.Fatal(err)
	}
}

func scheduleMaterialization(repo *dwh.Repository, channel *newsinergi.Repository, metrics *dwh.MetricStore, dashboard *dwh.DashboardService) {
	// ponytail: one scheduler per process; add a database lease if multiple replicas refresh together.
	refresh := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		latest, err := repo.LatestCommon(ctx)
		if err != nil {
			log.Printf("DWH watermark refresh failed: %v", err)
			return
		}
		dashboard.SetWatermark(latest)
		from, err := metrics.LatestDWH(ctx)
		if err != nil {
			log.Printf("materialization watermark failed: %v", err)
			return
		}
		if from.IsZero() {
			value := os.Getenv("MATERIALIZE_BACKFILL_FROM")
			if value == "" {
				return
			}
			from, err = time.Parse("2006-01-02", value)
			if err != nil {
				log.Printf("invalid MATERIALIZE_BACKFILL_FROM: %v", err)
				return
			}
		} else {
			from = from.AddDate(0, 0, 1)
		}
		if from.After(latest) {
			return
		}
		if err := metrics.Materialize(ctx, repo, channel, from, latest, false); err != nil {
			log.Printf("historical materialization failed: %v", err)
		}
	}
	refresh()
	ticker := time.NewTicker(duration("DASHBOARD_MATERIALIZE_INTERVAL", 30*time.Minute))
	defer ticker.Stop()
	for range ticker.C {
		refresh()
	}
}

func userCommand(command string, args []string) {
	store, err := users.Open(context.Background(), os.Getenv("APP_DBSTRING"))
	if err != nil {
		log.Fatal(err)
	}
	defer store.DB.Close()
	switch command {
	case "bootstrap-admin":
		if len(args) != 0 {
			log.Fatal("bootstrap-admin takes no arguments")
		}
		created, err := store.BootstrapAdmin(context.Background(), os.Getenv("BM_ADMIN_USERNAME"), os.Getenv("BM_ADMIN_PASSWORD"), os.Getenv("BM_ADMIN_NAME"))
		if err != nil {
			log.Fatal(err)
		}
		if created {
			log.Print("admin created")
		} else {
			log.Print("admin already exists; skipped")
		}
	case "seed-users":
		fs := flag.NewFlagSet("seed-users", flag.ExitOnError)
		path := fs.String("file", "Username BMOT.csv", "semicolon-delimited user CSV")
		if err := fs.Parse(args); err != nil {
			log.Fatal(err)
		}
		file, err := os.Open(*path)
		if err != nil {
			log.Fatal(err)
		}
		defer file.Close()
		result, err := store.SeedCSV(context.Background(), file)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("users created: %d; existing usernames skipped: %d", result.Created, result.Skipped)
	}
}
