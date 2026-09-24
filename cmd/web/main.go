package main

import (
	"bufio"
	"context"
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
	"github.com/ibldzn/dashboard-roro-jongrang/internal/service/realtime"
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
	source := os.Getenv("DASHBOARD_DATA_SOURCE")
	if source == "" {
		source = "mock"
	}
	var dashboard service.DashboardService
	var latestDate time.Time
	var closeDWH func() error
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
		if source == "dwh" {
			dashboard, latestDate = dwh.NewDashboardService(repo, latest), latest
			break
		}
		store, err = realtime.Open(context.Background(), os.Getenv("APP_DBSTRING"))
		if err != nil {
			log.Fatalf("application snapshot database unavailable: %v", err)
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
		dashboard = hybrid
		latestDate = realtime.Today()
		go func() {
			ticker := time.NewTicker(duration("DWH_WATERMARK_INTERVAL", time.Hour))
			defer ticker.Stop()
			for range ticker.C {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				day, err := repo.LatestCommon(ctx)
				cancel()
				if err != nil {
					log.Print("DWH watermark refresh failed")
					continue
				}
				hybrid.SetWatermark(day)
			}
		}()
		if os.Getenv("REALTIME_REFRESH_ENABLED") != "false" {
			go refresh.Schedule(context.Background(), duration("REALTIME_REFRESH_INTERVAL", 30*time.Minute))
		}
	default:
		log.Fatalf("unsupported DASHBOARD_DATA_SOURCE %q", source)
	}
	if closeDWH != nil {
		defer closeDWH()
	}
	if store != nil {
		defer store.Close()
	}
	renderer, err := view.New("web/templates")
	if err != nil {
		log.Fatal(err)
	}
	app := &handler.App{Service: dashboard, Sessions: middleware.NewSessions(), View: renderer, LatestDate: latestDate, Demo: source == "mock"}
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
