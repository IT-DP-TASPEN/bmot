package mock

import (
	"context"
	"testing"

	"github.com/ibldzn/dashboard-roro-jongrang/internal/domain"
)

func TestReconciliationAndFilters(t *testing.T) {
	s := NewDashboardService()
	seen := make(map[string]bool)
	for _, a := range s.accounts {
		if seen[a.number] {
			t.Fatalf("duplicate account %s", a.number)
		}
		seen[a.number] = true
	}
	ctx := context.Background()
	for _, spec := range []struct{ mode, period string }{{"daily", "2026-09-24"}, {"monthly", "2026-09"}, {"yearly", "2026"}} {
		all, _ := domain.ParseFilter(spec.mode, spec.period, "ALL")
		for _, tc := range []struct{ kind, category, key string }{
			{"tabungan", "dpk", "balance"}, {"tabungan", "abp", "balance"},
			{"deposito", "dpk", "balance"}, {"deposito", "abp", "balance"},
			{"kredit", "organik", "booking"}, {"kredit", "organik", "bade"},
			{"kredit", "channeling", "booking"}, {"kredit", "channeling", "bade"},
		} {
			var branchSum int64
			for i := 0; i <= 8; i++ {
				f := all
				f.Branch = string([]byte{'0', '0', byte('0' + i)})
				branchSum += s.sum(f, tc.kind, tc.category, tc.key)
			}
			total := s.sum(all, tc.kind, tc.category, tc.key)
			if total != branchSum {
				t.Fatalf("%s %s %s branch sum %d != %d", tc.kind, tc.category, tc.key, branchSum, total)
			}
			n, err := s.GetNominative(ctx, domain.NominativeFilter{Filter: all, Domain: tc.kind, Category: tc.category, Metric: tc.key})
			if err != nil {
				t.Fatal(err)
			}
			if n.Total != total {
				t.Fatalf("%s %s %s nominative %d != %d", tc.kind, tc.category, tc.key, n.Total, total)
			}
		}
	}
}

func TestMaturityBucketsAndSearch(t *testing.T) {
	s := NewDashboardService()
	f, _ := domain.ParseFilter("monthly", "2026-09", "ALL")
	d, _ := s.GetDepositMaturities(context.Background(), f)
	var total int64
	for _, g := range d.Groups {
		for _, m := range g.Metrics {
			n, err := s.GetNominative(context.Background(), domain.NominativeFilter{Filter: f, Domain: "deposito", Category: "jatuh-tempo", Metric: m.Key, Bucket: m.Bucket})
			if err != nil {
				t.Fatal(err)
			}
			if n.Total != m.Value {
				t.Fatalf("bucket %s %s: %d != %d", m.Bucket, m.Key, n.Total, m.Value)
			}
			if m.Key == "balance" {
				total += m.Value
			}
		}
	}
	if total != s.sum(f, "deposito", "", "balance") {
		t.Fatal("maturity nominal does not equal deposit balance")
	}
	n, err := s.GetNominative(context.Background(), domain.NominativeFilter{Filter: f, Domain: "kredit", Category: "organik", Metric: "bade", Search: "C001", Page: 2})
	if err != nil {
		t.Fatal(err)
	}
	if n.FilterCount == 0 || n.Filtered >= n.Total {
		t.Fatal("search did not filter")
	}
	if len(n.Rows) > 15 {
		t.Fatal("pagination exceeded page size")
	}
}

func TestOverviewTotalsReconcile(t *testing.T) {
	s := NewDashboardService()
	f, _ := domain.ParseFilter("monthly", "2026-09", "ALL")
	o, _ := s.GetOverview(context.Background(), f)
	for _, m := range o.Metrics {
		n, err := s.GetNominative(context.Background(), domain.NominativeFilter{Filter: f, Domain: m.Domain, Category: m.Category, Metric: m.Key})
		if err != nil {
			t.Fatal(err)
		}
		if n.Total != m.Value {
			t.Fatalf("%s: %d != %d", m.Label, n.Total, m.Value)
		}
	}
}

func TestComparisonAndTrend(t *testing.T) {
	s := NewDashboardService()
	f, _ := domain.ParseFilter("monthly", "2026-09", "001")
	d, _ := s.GetLoans(context.Background(), f)
	if len(d.Groups) != 2 || len(d.Series) != 4 {
		t.Fatalf("loan groups or trends changed: %+v", d)
	}
	for i, category := range []string{"organik", "channeling"} {
		if len(d.Groups[i].Metrics) != 2 {
			t.Fatalf("%s has %d metrics", category, len(d.Groups[i].Metrics))
		}
		position := "bade"
		if category == "channeling" {
			position = "plafond"
		}
		for j, key := range []string{"booking", position} {
			metric := d.Groups[i].Metrics[j]
			points := d.Series[i*2+j].Points
			if metric.Key != key || metric.Value != s.sum(f, "kredit", category, key) || metric.Previous != s.sum(domain.Previous(f), "kredit", category, key) {
				t.Fatalf("%s %s did not reconcile: %+v", category, key, metric)
			}
			if len(points) != 12 || points[11].Value != metric.Value || points[0].Label == points[11].Label {
				t.Fatalf("%s %s trend did not reconcile: %+v", category, key, points)
			}
		}
	}
	if d.Groups[0].Metrics[0].Value == d.Groups[0].Metrics[0].Previous {
		t.Fatal("booking mock trend did not change")
	}
	if _, err := s.GetNominative(context.Background(), domain.NominativeFilter{Filter: f, Domain: "kredit", Category: "organik", Metric: "limit"}); err == nil {
		t.Fatal("limit drill-down is still available")
	}
}
