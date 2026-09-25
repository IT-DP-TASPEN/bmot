package mock

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ibldzn/dashboard-roro-jongrang/internal/domain"
)

type DashboardService struct{ accounts []account }

func NewDashboardService() *DashboardService { return &DashboardService{accounts: seed()} }

func available(f domain.Filter) bool {
	return !f.Date.Before(domain.FirstMockDate) && !f.Date.After(domain.LastMockDate)
}

func (s *DashboardService) each(f domain.Filter, fn func(account)) {
	for _, a := range s.accounts {
		if f.Branch == "ALL" || a.branch == f.Branch {
			fn(a)
		}
	}
}

func (s *DashboardService) sum(f domain.Filter, kind, category, metric string) int64 {
	var total int64
	s.each(f, func(a account) {
		if a.kind != kind || category != "" && a.category != category {
			return
		}
		switch metric {
		case "balance", "bade":
			total += balance(a, f.Date)
		case "plafond":
			if !f.Date.Before(domain.FirstMockDate) {
				total += a.base
			}
		case "booking":
			total += booking(a, domain.PeriodStart(f), f.Date)
		case "noa":
			total++
		}
	})
	return total
}

func (s *DashboardService) metric(f domain.Filter, label, kind, category, key, unit string) domain.Metric {
	queryCategory := category
	if category == "" {
		category = "all"
	}
	return domain.Metric{Label: label, Value: s.sum(f, kind, queryCategory, key), Previous: s.sum(domain.Previous(f), kind, queryCategory, key), HasPrevious: available(domain.Previous(f)), Unit: unit, Domain: kind, Category: category, Key: key}
}

func shift(f domain.Filter, n int) domain.Filter {
	if f.Mode == "daily" {
		f.Date = f.Date.AddDate(0, 0, n)
		return f
	}
	if f.Mode == "yearly" {
		f.Date = f.Date.AddDate(n, 0, 0)
		return f
	}
	start := time.Date(f.Date.Year(), f.Date.Month()+time.Month(n), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, -1)
	currentEnd := time.Date(f.Date.Year(), f.Date.Month()+1, 0, 0, 0, 0, 0, time.UTC)
	day := f.Date.Day()
	if day >= currentEnd.Day() || day > end.Day() {
		day = end.Day()
	}
	f.Date = time.Date(start.Year(), start.Month(), day, 0, 0, 0, 0, time.UTC)
	return f
}

func (s *DashboardService) trend(f domain.Filter, kind, category, key, name string) domain.Series {
	out := domain.Series{Name: name}
	for i := 11; i >= 0; i-- {
		p := shift(f, -i)
		if p.Date.Before(domain.FirstMockDate) {
			continue
		}
		label := p.Date.Format("Jan 06")
		if f.Mode == "daily" {
			label = p.Date.Format("02 Jan")
		}
		if f.Mode == "yearly" {
			label = p.Date.Format("2006")
		}
		out.Points = append(out.Points, domain.Point{Label: label, Value: s.sum(p, kind, category, key)})
	}
	return out
}

func (s *DashboardService) GetOverview(_ context.Context, f domain.Filter) (domain.Dashboard, error) {
	if !available(f) {
		return domain.Dashboard{Title: "Overview", Subtitle: "Ringkasan posisi dan kinerja bank", Empty: true}, nil
	}
	loans := s.metric(f, "Outstanding Kredit", "kredit", "", "bade", "rupiah")
	return domain.Dashboard{Title: "Overview", Subtitle: "Ringkasan posisi dan kinerja bank", Metrics: []domain.Metric{
		s.metric(f, "Total Tabungan", "tabungan", "", "balance", "rupiah"),
		s.metric(f, "Total Deposito", "deposito", "", "balance", "rupiah"), loans,
	}, Secondary: []domain.Metric{s.ratio(f, "LDR"), s.ratio(f, "NPL"), s.ratio(f, "Cash Ratio"), s.ratio(f, "NIM")}, Series: []domain.Series{
		s.trend(f, "tabungan", "", "balance", "Tabungan"), s.trend(f, "deposito", "", "balance", "Deposito"),
		s.trend(f, "kredit", "organik", "bade", "Organik"), s.trend(f, "kredit", "channeling", "bade", "Channeling"),
	}}, nil
}

func (s *DashboardService) GetSavings(_ context.Context, f domain.Filter, category string) (domain.Dashboard, error) {
	if category != "dpk" && category != "abp" {
		return domain.Dashboard{}, errors.New("kategori tabungan tidak valid")
	}
	if !available(f) {
		return domain.Dashboard{Title: "Tabungan", Subtitle: strings.ToUpper(category), Empty: true}, nil
	}
	bal := s.metric(f, "Saldo Tabungan", "tabungan", category, "balance", "rupiah")
	noa := s.metric(f, "Jumlah Rekening", "tabungan", category, "noa", "count")
	avg := domain.Metric{Label: "Rata-rata Saldo", Unit: "rupiah", HasPrevious: bal.HasPrevious && noa.HasPrevious}
	if noa.Value > 0 {
		avg.Value = bal.Value / noa.Value
		avg.Previous = bal.Previous / noa.Previous
	}
	return domain.Dashboard{Title: "Tabungan", Subtitle: strings.ToUpper(category), Metrics: []domain.Metric{bal, noa, avg}, Series: []domain.Series{s.trend(f, "tabungan", category, "balance", "Saldo")}}, nil
}

func (s *DashboardService) GetDeposits(_ context.Context, f domain.Filter, category string) (domain.Dashboard, error) {
	if category != "dpk" && category != "abp" {
		return domain.Dashboard{}, errors.New("kategori deposito tidak valid")
	}
	if !available(f) {
		return domain.Dashboard{Title: "Deposito", Subtitle: strings.ToUpper(category), Empty: true}, nil
	}
	bal := s.metric(f, "Nominal Deposito", "deposito", category, "balance", "rupiah")
	noa := s.metric(f, "Jumlah Rekening", "deposito", category, "noa", "count")
	return domain.Dashboard{Title: "Deposito", Subtitle: strings.ToUpper(category), Metrics: []domain.Metric{bal, noa}, Series: []domain.Series{s.trend(f, "deposito", category, "balance", "Nominal")}}, nil
}

func (s *DashboardService) maturities(f domain.Filter) []domain.Maturity {
	var rows []domain.Maturity
	s.each(f, func(a account) {
		if a.kind == "deposito" {
			rows = append(rows, domain.Maturity{Name: a.name, Account: a.number, Branch: a.branch, Due: dueDate(a, f.Date), Amount: balance(a, f.Date)})
		}
	})
	sort.Slice(rows, func(i, j int) bool { return rows[i].Due.Before(rows[j].Due) })
	return rows
}

func (s *DashboardService) GetDepositMaturities(_ context.Context, f domain.Filter) (domain.Dashboard, error) {
	if !available(f) {
		return domain.Dashboard{Title: "Deposito", Subtitle: "Jatuh Tempo", Empty: true}, nil
	}
	rows := s.maturities(f)
	buckets := []struct {
		name     string
		key      string
		min, max int
	}{{"≤ 7 hari", "0-7", 0, 7}, {"8–30 hari", "8-30", 8, 30}, {"> 30 hari", "31+", 31, 9999}}
	d := domain.Dashboard{Title: "Deposito", Subtitle: "Jatuh Tempo", Maturities: rows}
	for _, b := range buckets {
		var count, amount int64
		for _, r := range rows {
			days := int(r.Due.Sub(f.Date).Hours() / 24)
			if days >= b.min && days <= b.max {
				count++
				amount += r.Amount
			}
		}
		d.Groups = append(d.Groups, domain.Group{Title: b.name, Metrics: []domain.Metric{{Label: "NOA", Value: count, Unit: "count", Domain: "deposito", Category: "jatuh-tempo", Key: "noa", Bucket: b.key}, {Label: "Nominal", Value: amount, Unit: "rupiah", Domain: "deposito", Category: "jatuh-tempo", Key: "balance", Bucket: b.key}}})
	}
	series := domain.Series{Name: "Nominal jatuh tempo"}
	for week := 0; week < 12; week++ {
		var total int64
		for _, r := range rows {
			days := int(r.Due.Sub(f.Date).Hours() / 24)
			if days >= week*7 && days < (week+1)*7 {
				total += r.Amount
			}
		}
		series.Points = append(series.Points, domain.Point{Label: fmt.Sprintf("Minggu %d", week+1), Value: total})
	}
	d.Series = []domain.Series{series}
	return d, nil
}

func (s *DashboardService) GetLoans(_ context.Context, f domain.Filter) (domain.Dashboard, error) {
	if !available(f) {
		return domain.Dashboard{Title: "Kredit", Subtitle: "Channeling dan Organik", Empty: true}, nil
	}
	d := domain.Dashboard{Title: "Kredit", Subtitle: "Channeling dan Organik"}
	for _, cat := range []string{"organik", "channeling"} {
		label := strings.ToUpper(cat)
		position, positionLabel := "bade", "BADE"
		if cat == "channeling" {
			position, positionLabel = "plafond", "Plafond"
		}
		g := domain.Group{Title: label, Metrics: []domain.Metric{
			s.metric(f, "Booking", "kredit", cat, "booking", "rupiah"),
			s.metric(f, positionLabel, "kredit", cat, position, "rupiah"),
		}}
		d.Groups = append(d.Groups, g)
		d.Series = append(d.Series, s.trend(f, "kredit", cat, "booking", label+" Booking"), s.trend(f, "kredit", cat, position, label+" "+positionLabel))
	}
	return d, nil
}

type financialFacts struct{ funding, loan, bad, cash, netInterest, earningAssets, operatingIncome, operatingExpense, assets, profit int64 }

func (s *DashboardService) facts(f domain.Filter) financialFacts {
	loan := s.sum(f, "kredit", "", "bade")
	funding := s.sum(f, "tabungan", "", "balance") + s.sum(f, "deposito", "", "balance")
	var bad int64
	s.each(f, func(a account) {
		if a.kind == "kredit" && a.collectibility == "Kurang Lancar" {
			bad += balance(a, f.Date)
		}
	})
	// Illustrative mock financial facts. Formal DWH account mappings are an owner decision.
	interestIncome := loan * 12 / 100
	interestExpense := funding * 4 / 100
	opIncome := interestIncome + loan/50
	opExpense := interestExpense + opIncome*42/100
	days := int64(f.Date.Sub(domain.PeriodStart(f)).Hours()/24) + 1
	if days < 0 {
		days = 0
	}
	return financialFacts{funding: funding, loan: loan, bad: bad, cash: funding / 7, netInterest: interestIncome - interestExpense, earningAssets: loan, operatingIncome: opIncome, operatingExpense: opExpense, assets: funding + funding/6, profit: (opIncome - opExpense) * days / 365}
}

func (s *DashboardService) ratioValue(f domain.Filter, key string) int64 {
	x := s.facts(f)
	switch key {
	case "LDR":
		if x.funding > 0 {
			return x.loan * 10000 / x.funding
		}
	case "NPL":
		if x.loan > 0 {
			return x.bad * 10000 / x.loan
		}
	case "Cash Ratio":
		if x.funding > 0 {
			return x.cash * 10000 / x.funding
		}
	case "NIM":
		if x.earningAssets > 0 {
			return x.netInterest * 10000 / x.earningAssets
		}
	case "BOPO":
		if x.operatingIncome > 0 {
			return x.operatingExpense * 10000 / x.operatingIncome
		}
	}
	return 0
}

func (s *DashboardService) ratio(f domain.Filter, name string) domain.Metric {
	return domain.Metric{Label: name, Value: s.ratioValue(f, name), Previous: s.ratioValue(domain.Previous(f), name), HasPrevious: available(domain.Previous(f)), Unit: "percent"}
}

func (s *DashboardService) GetFinancialPerformance(_ context.Context, f domain.Filter) (domain.Dashboard, error) {
	if !available(f) {
		return domain.Dashboard{Title: "Kinerja Keuangan", Subtitle: "Rasio dan nominal", Empty: true}, nil
	}
	r := domain.Group{Title: "Rasio"}
	for _, name := range []string{"NIM", "LDR", "BOPO", "Cash Ratio", "NPL"} {
		r.Metrics = append(r.Metrics, s.ratio(f, name))
	}
	x := s.facts(f)
	previous := s.facts(domain.Previous(f))
	n := domain.Group{Title: "Nominal Keuangan", Metrics: []domain.Metric{
		{Label: "Aset", Value: x.assets, Previous: previous.assets, HasPrevious: available(domain.Previous(f)), Unit: "rupiah"},
		{Label: "Laba Sebelum Pajak", Value: x.profit, Previous: previous.profit, HasPrevious: available(domain.Previous(f)), Unit: "rupiah"},
	}}
	assets := domain.Series{Name: "Aset"}
	profit := domain.Series{Name: "Laba"}
	for i := 11; i >= 0; i-- {
		p := shift(f, -i)
		if p.Date.Before(domain.FirstMockDate) {
			continue
		}
		fact := s.facts(p)
		label := p.Date.Format("Jan 06")
		if f.Mode == "daily" {
			label = p.Date.Format("02 Jan")
		}
		if f.Mode == "yearly" {
			label = p.Date.Format("2006")
		}
		assets.Points = append(assets.Points, domain.Point{Label: label, Value: fact.assets})
		profit.Points = append(profit.Points, domain.Point{Label: label, Value: fact.profit})
	}
	return domain.Dashboard{Title: "Kinerja Keuangan", Subtitle: "Rasio dan nominal", Groups: []domain.Group{r, n}, Series: []domain.Series{assets, profit}}, nil
}

func (s *DashboardService) GetNominative(_ context.Context, f domain.NominativeFilter) (domain.NominativeResult, error) {
	allowed := map[string]map[string]bool{"tabungan": {"balance": true, "noa": true}, "deposito": {"balance": true, "noa": true}, "kredit": {"booking": true, "bade": true}}
	if !allowed[f.Domain][f.Metric] {
		return domain.NominativeResult{}, errors.New("metrik nominatif tidak valid")
	}
	if f.Domain == "kredit" && f.Category != "organik" && f.Category != "channeling" && f.Category != "all" {
		return domain.NominativeResult{}, errors.New("kategori kredit tidak valid")
	}
	if f.Domain == "tabungan" && f.Category != "dpk" && f.Category != "abp" && f.Category != "all" {
		return domain.NominativeResult{}, errors.New("kategori tabungan tidak valid")
	}
	if f.Domain == "deposito" && f.Category != "dpk" && f.Category != "abp" && f.Category != "jatuh-tempo" && f.Category != "all" {
		return domain.NominativeResult{}, errors.New("kategori deposito tidak valid")
	}
	if f.Category == "jatuh-tempo" && f.Metric != "balance" && f.Metric != "noa" {
		return domain.NominativeResult{}, errors.New("metrik jatuh tempo tidak valid")
	}
	if f.Bucket != "" && f.Bucket != "0-7" && f.Bucket != "8-30" && f.Bucket != "31+" {
		return domain.NominativeResult{}, errors.New("rentang jatuh tempo tidak valid")
	}
	if f.Bucket != "" && f.Category != "jatuh-tempo" {
		return domain.NominativeResult{}, errors.New("rentang hanya untuk jatuh tempo")
	}
	metricLabel := map[string]string{"balance": "Saldo", "bade": "BADE", "booking": "Booking", "noa": "NOA"}[f.Metric]
	if f.Domain == "deposito" && f.Metric == "balance" {
		metricLabel = "Nominal"
	}
	res := domain.NominativeResult{Title: fmt.Sprintf("Nominatif %s %s", strings.Title(f.Domain), strings.ToUpper(f.Category)), MetricLabel: metricLabel}
	var rows []domain.Record
	if available(f.Filter) {
		s.each(f.Filter, func(a account) {
			if a.kind != f.Domain || f.Category != "jatuh-tempo" && f.Category != "all" && a.category != f.Category {
				return
			}
			if f.Bucket != "" {
				days := int(dueDate(a, f.Date).Sub(f.Date).Hours() / 24)
				if f.Bucket == "0-7" && days > 7 || f.Bucket == "8-30" && (days < 8 || days > 30) || f.Bucket == "31+" && days < 31 {
					return
				}
			}
			amount := balance(a, f.Date)
			if f.Metric == "booking" {
				amount = booking(a, domain.PeriodStart(f.Filter), f.Date)
			}
			if f.Metric == "noa" {
				amount = 1
			}
			row := domain.Record{Name: a.name, Account: a.number, CIF: a.cif, Branch: a.branch, Product: a.product, Collectibility: a.collectibility, Amount: amount, Outstanding: balance(a, f.Date), Due: dueDate(a, f.Date)}
			rows = append(rows, row)
			res.Total += amount
		})
	}
	res.Count = len(rows)
	q := strings.ToLower(strings.TrimSpace(f.Search))
	for _, row := range rows {
		if q == "" || strings.Contains(strings.ToLower(row.Name+" "+row.Account+" "+row.CIF+" "+row.Branch), q) {
			res.FilterCount++
			res.Filtered += row.Amount
			res.Rows = append(res.Rows, row)
		}
	}
	const size = 15
	res.Pages = (res.FilterCount + size - 1) / size
	if res.Pages == 0 {
		res.Pages = 1
	}
	if f.Page < 1 {
		f.Page = 1
	}
	if f.Page > res.Pages {
		f.Page = res.Pages
	}
	res.Page = f.Page
	from := (f.Page - 1) * size
	to := from + size
	if to > len(res.Rows) {
		to = len(res.Rows)
	}
	res.Rows = res.Rows[from:to]
	return res, nil
}
