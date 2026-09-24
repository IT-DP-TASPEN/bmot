package domain

import (
	"errors"
	"strconv"
	"time"
)

var LastMockDate = time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
var FirstMockDate = time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC)

func ParseFilter(mode, period, branch string) (Filter, error) {
	if mode == "" {
		mode = "monthly"
	}
	if branch == "" {
		branch = "ALL"
	}
	if branch != "ALL" {
		n, err := strconv.Atoi(branch)
		if err != nil || len(branch) != 3 || n < 0 || n > 8 {
			return Filter{}, errors.New("cabang tidak valid")
		}
	}
	var layout string
	switch mode {
	case "daily":
		layout = "2006-01-02"
		if period == "" {
			period = LastMockDate.Format(layout)
		}
	case "monthly":
		layout = "2006-01"
		if period == "" {
			period = LastMockDate.Format(layout)
		}
	case "yearly":
		layout = "2006"
		if period == "" {
			period = LastMockDate.Format(layout)
		}
	default:
		return Filter{}, errors.New("mode periode tidak valid")
	}
	d, err := time.Parse(layout, period)
	if err != nil || d.Format(layout) != period {
		return Filter{}, errors.New("periode tidak valid")
	}
	if mode == "monthly" {
		d = d.AddDate(0, 1, -1)
	}
	if mode == "yearly" {
		d = time.Date(d.Year(), 12, 31, 0, 0, 0, 0, time.UTC)
	}
	if d.After(LastMockDate) && (mode == "monthly" && period == LastMockDate.Format("2006-01") || mode == "yearly" && period == LastMockDate.Format("2006")) {
		d = LastMockDate
	}
	return Filter{Mode: mode, Period: period, Date: d, Branch: branch}, nil
}

func Previous(f Filter) Filter {
	p := f
	switch f.Mode {
	case "daily":
		p.Date = f.Date.AddDate(0, 0, -1)
	case "monthly":
		// Match the cutoff day for an open month, otherwise use the prior month end.
		monthEnd := time.Date(f.Date.Year(), f.Date.Month()+1, 0, 0, 0, 0, 0, time.UTC)
		if f.Date.Before(monthEnd) {
			p.Date = time.Date(f.Date.Year(), f.Date.Month(), 0, 0, 0, 0, 0, time.UTC)
			if f.Date.Day() < p.Date.Day() {
				p.Date = time.Date(p.Date.Year(), p.Date.Month(), f.Date.Day(), 0, 0, 0, 0, time.UTC)
			}
		} else {
			p.Date = time.Date(f.Date.Year(), f.Date.Month(), 0, 0, 0, 0, 0, time.UTC)
		}
	case "yearly":
		p.Date = f.Date.AddDate(-1, 0, 0)
	}
	return p
}

func PeriodStart(f Filter) time.Time {
	switch f.Mode {
	case "daily":
		return f.Date
	case "monthly":
		return time.Date(f.Date.Year(), f.Date.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		return time.Date(f.Date.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	}
}
