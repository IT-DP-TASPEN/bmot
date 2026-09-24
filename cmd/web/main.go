package main

import (
	"bufio"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/ibldzn/dashboard-roro-jongrang/internal/http/handler"
	"github.com/ibldzn/dashboard-roro-jongrang/internal/http/middleware"
	"github.com/ibldzn/dashboard-roro-jongrang/internal/service"
	"github.com/ibldzn/dashboard-roro-jongrang/internal/service/mock"
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
	switch source {
	case "mock":
		dashboard = mock.NewDashboardService()
	default:
		log.Fatalf("unsupported DASHBOARD_DATA_SOURCE %q", source)
	}
	renderer, err := view.New("web/templates")
	if err != nil {
		log.Fatal(err)
	}
	app := &handler.App{Service: dashboard, Sessions: middleware.NewSessions(), View: renderer}
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("dashboard listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, app.Routes()))
}
