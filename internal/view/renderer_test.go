package view

import (
	"bytes"
	"html"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ibldzn/dashboard-roro-jongrang/internal/domain"
)

func TestIndonesianFormatting(t *testing.T) {
	if got := Rupiah(128_400_000_000); got != "Rp 128,4 M" {
		t.Fatalf("money: %s", got)
	}
	if got := Rupiah(8_210_000_000); got != "Rp 8,21 M" {
		t.Fatalf("money: %s", got)
	}
	if got := Percent(8142); got != "81,42%" {
		t.Fatalf("percent: %s", got)
	}
	if got := Integer(1248); got != "1.248" {
		t.Fatalf("integer: %s", got)
	}
}

func TestMetricComparison(t *testing.T) {
	for _, tc := range []struct {
		name, label, unit string
		previous, current int64
		want, class       string
	}{
		{"NPL down", "NPL", "percent", 500, 400, "−1,00 pp", "change-positive"},
		{"NPL up", "NPL", "percent", 400, 460, "+0,60 pp", "change-negative"},
		{"BOPO down", "BOPO", "percent", 8330, 8210, "−1,20 pp", "change-positive"},
		{"BOPO up", "BOPO", "percent", 8210, 8330, "+1,20 pp", "change-negative"},
		{"NIM up", "NIM", "percent", 820, 875, "+0,55 pp", "change-positive"},
		{"NIM down", "NIM", "percent", 875, 820, "−0,55 pp", "change-negative"},
		{"Cash Ratio up", "Cash Ratio", "percent", 1920, 2130, "+2,10 pp", "change-positive"},
		{"Cash Ratio down", "Cash Ratio", "percent", 2130, 1920, "−2,10 pp", "change-negative"},
		{"NPL unchanged", "NPL", "percent", 400, 400, "0,00 pp", "change-neutral"},
		{"LDR up", "LDR", "percent", 8000, 8500, "+5,00 pp", "change-neutral"},
		{"LDR down", "LDR", "percent", 8500, 8000, "−5,00 pp", "change-neutral"},
		{"Aset up", "Aset", "rupiah", 100_000_000, 110_000_000, "+Rp 10 Jt", "change-neutral"},
		{"Aset down", "Aset", "rupiah", 110_000_000, 100_000_000, "−Rp 10 Jt", "change-neutral"},
		{"Aset zero baseline", "Aset", "rupiah", 0, 10_000_000, "+Rp 10 Jt", "change-neutral"},
		{"Aset unchanged", "Aset", "rupiah", 10_000_000, 10_000_000, "Rp 0", "change-neutral"},
		{"Aset compact thousands", "Aset", "rupiah", 0, 750_000, "+Rp 750 Rb", "change-neutral"},
		{"Aset compact millions", "Aset", "rupiah", 2_150_000, 0, "−Rp 2,15 Jt", "change-neutral"},
		{"Aset compact billions", "Aset", "rupiah", 0, 4_200_000_000, "+Rp 4,2 M", "change-neutral"},
		{"NOA up", "NOA", "count", 36_526, 36_891, "+365 rekening", "change-neutral"},
		{"NOA down", "NOA", "count", 1_500, 1_250, "−250 rekening", "change-neutral"},
		{"NOA thousands", "NOA", "count", 0, 1_250, "+1.250 rekening", "change-neutral"},
		{"NOA unchanged", "NOA", "count", 1_250, 1_250, "0 rekening", "change-neutral"},
		{"NIM zero baseline", "NIM", "percent", 0, 250, "+2,50 pp", "change-positive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, class := comparison(domain.Metric{Label: tc.label, Unit: tc.unit, Value: tc.current, Previous: tc.previous, HasPrevious: true})
			if got != tc.want || class != tc.class {
				t.Fatalf("comparison=%q %q, want %q %q", got, class, tc.want, tc.class)
			}
		})
	}
}

func TestMetricTemplatePreviousAvailability(t *testing.T) {
	r, err := New(filepath.Join("..", "..", "web", "templates"))
	if err != nil {
		t.Fatal(err)
	}
	metricView := FuncMap()["metricView"].(func(domain.Filter, domain.Metric) any)
	for _, tc := range []struct {
		name, unit string
		value      int64
		has        bool
		want       string
	}{
		{"Aset", "rupiah", 10_000_000, true, "+Rp 10 Jt</span> vs periode sebelumnya"},
		{"NPL", "percent", 250, true, "+2,50 pp</span> vs periode sebelumnya"},
		{"Aset", "rupiah", 100, false, "Posisi terpilih"},
	} {
		var out bytes.Buffer
		m := domain.Metric{Label: tc.name, Unit: tc.unit, Value: tc.value, Previous: 0, HasPrevious: tc.has}
		if err := r.templates["dashboard"].ExecuteTemplate(&out, "metric", metricView(domain.Filter{}, m)); err != nil {
			t.Fatal(err)
		}
		markup := html.UnescapeString(out.String())
		if !strings.Contains(markup, tc.want) || (!tc.has && strings.Contains(markup, "vs periode sebelumnya")) {
			t.Fatalf("unexpected metric markup: %s", out.String())
		}
	}
}
