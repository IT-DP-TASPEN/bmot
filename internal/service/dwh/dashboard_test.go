package dwh

import (
	"context"
	"testing"
	"time"

	"github.com/ibldzn/dashboard-roro-jongrang/internal/domain"
)

func TestHybridSourceRoutingAndWatermark(t *testing.T) {
	today := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	historical, realtime := &Repository{}, &Repository{}
	s := NewHybridDashboardService(historical, realtime, today.AddDate(0, 0, -1), nil, time.Hour)
	s.now = func() time.Time { return today.Add(10 * time.Hour) }
	if s.sourceFor(today) != realtime || s.sourceFor(today.AddDate(0, 0, -1)) != historical {
		t.Fatal("wrong source routing")
	}
	s.SetWatermark(today)
	if s.sourceFor(today) != historical {
		t.Fatal("DWH today should be authoritative when complete")
	}
}

func TestHybridFreshness(t *testing.T) {
	today := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	s := NewHybridDashboardService(&Repository{}, &Repository{}, today.AddDate(0, 0, -1), func(context.Context) (time.Time, time.Time, bool, error) {
		return today, today.Add(8 * time.Hour), true, nil
	}, time.Hour)
	s.now = func() time.Time { return today.Add(10 * time.Hour) }
	p, err := s.Provenance(context.Background(), domain.Filter{Date: today})
	if err != nil || p.Kind != "realtime" || !p.Stale {
		t.Fatalf("provenance=%+v err=%v", p, err)
	}
	s.snapshot = func(context.Context) (time.Time, time.Time, bool, error) { return time.Time{}, time.Time{}, false, nil }
	p, err = s.Provenance(context.Background(), domain.Filter{Date: today})
	if err != nil || !p.Unavailable {
		t.Fatalf("missing snapshot provenance=%+v err=%v", p, err)
	}
}

func TestComponentAggregationAndTrendDates(t *testing.T) {
	day := time.Date(2026, 1, 24, 0, 0, 0, 0, time.UTC)
	f := domain.Filter{Mode: "daily", Date: day, Branch: "ALL"}
	x := snapshot{balance: map[string]balancePosition{key(day): {
		"1": 1000, "323": 70, "558": 10, "5": 100, "4": 200,
		"401": 30, "501": 10, "110": 100, "121": 100, "122": 50,
		"221": 100, "2312200": 100, "2212111": 20,
		"100": 20, "111": 20, "112": 10, "211": 50,
	}}, loans: map[string]loanPosition{key(day): {Outstanding: 100, Bad: 10}}}
	got := x.financial(f)
	if got["LDR"] != percent(150, 180) || got["NPL"] != percent(10, 100) || got["BOPO"] != percent(90, 200) || got["NIM"] != percent(240, 200) {
		t.Fatalf("financial=%v", got)
	}
	for i, p := range points(f) {
		if i > 0 && !p.Date.After(points(f)[i-1].Date) {
			t.Fatal("trend dates are not strictly increasing")
		}
	}
	if len(reportDates(f)) != 12 {
		t.Fatal("report dates should deduplicate trend and previous dates")
	}
}

func TestPreviousMetricAvailabilityUsesSnapshotPresence(t *testing.T) {
	day := time.Date(2026, 1, 24, 0, 0, 0, 0, time.UTC)
	f := domain.Filter{Mode: "daily", Date: day}
	prior := key(domain.Previous(f).Date)
	x := snapshot{
		savings:         map[string]fundingPosition{prior: {}},
		cachedFinancial: map[string]map[string]int64{prior: {"NPL": 0}},
	}
	if m := x.fundingMetric(f, x.savings, "tabungan", "all", "balance", "Tabungan", "rupiah"); !m.HasPrevious || m.Previous != 0 {
		t.Fatalf("zero funding snapshot should be available: %+v", m)
	}
	if m := x.ratio(f, "NPL"); !m.HasPrevious || m.Previous != 0 {
		t.Fatalf("zero ratio snapshot should be available: %+v", m)
	}
	delete(x.savings, prior)
	delete(x.cachedFinancial, prior)
	if m := x.fundingMetric(f, x.savings, "tabungan", "all", "balance", "Tabungan", "rupiah"); m.HasPrevious {
		t.Fatalf("missing funding snapshot should be unavailable: %+v", m)
	}
	if m := x.ratio(f, "NPL"); m.HasPrevious {
		t.Fatalf("missing ratio snapshot should be unavailable: %+v", m)
	}
}
