package domain

import "testing"

func TestFilterCutoffs(t *testing.T) {
	cases := []struct{ mode, period, current, previous string }{
		{"daily", "2026-09-24", "2026-09-24", "2026-09-23"},
		{"monthly", "2026-09", "2026-09-24", "2026-08-24"},
		{"monthly", "2026-08", "2026-08-31", "2026-07-31"},
		{"yearly", "2026", "2026-09-24", "2025-09-24"},
	}
	for _, tc := range cases {
		f, err := ParseFilter(tc.mode, tc.period, "ALL")
		if err != nil {
			t.Fatal(err)
		}
		if got := f.Date.Format("2006-01-02"); got != tc.current {
			t.Errorf("%s current %s", tc.mode, got)
		}
		if got := Previous(f).Date.Format("2006-01-02"); got != tc.previous {
			t.Errorf("%s previous %s", tc.mode, got)
		}
	}
	if _, err := ParseFilter("weekly", "2026-09", "ALL"); err == nil {
		t.Fatal("accepted invalid mode")
	}
	if _, err := ParseFilter("monthly", "2026-09", "999"); err == nil {
		t.Fatal("accepted invalid branch")
	}
}
