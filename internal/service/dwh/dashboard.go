package dwh

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/ibldzn/dashboard-roro-jongrang/internal/domain"
)

type DashboardService struct{ repo *Repository }

func NewDashboardService(repo *Repository) *DashboardService { return &DashboardService{repo: repo} }

func key(t time.Time) string { return t.Format("2006-01-02") }

func shift(f domain.Filter, n int) domain.Filter {
	if f.Mode == "daily" {
		f.Date = f.Date.AddDate(0, 0, n)
	} else if f.Mode == "yearly" {
		f.Date = f.Date.AddDate(n, 0, 0)
	} else {
		first := time.Date(f.Date.Year(), f.Date.Month()+time.Month(n), 1, 0, 0, 0, 0, time.UTC)
		end := first.AddDate(0, 1, -1)
		currentEnd := time.Date(f.Date.Year(), f.Date.Month()+1, 0, 0, 0, 0, 0, time.UTC)
		day := f.Date.Day()
		if day == currentEnd.Day() || day > end.Day() {
			day = end.Day()
		}
		f.Date = time.Date(first.Year(), first.Month(), day, 0, 0, 0, 0, time.UTC)
	}
	return f
}

func points(f domain.Filter) []domain.Filter {
	out := make([]domain.Filter, 12)
	for i := range out {
		out[i] = shift(f, i-11)
	}
	return out
}

func label(f domain.Filter) string {
	switch f.Mode {
	case "daily":
		return f.Date.Format("02 Jan")
	case "yearly":
		return f.Date.Format("2006")
	default:
		return f.Date.Format("Jan 06")
	}
}

func allDates(filters ...domain.Filter) []time.Time {
	seen := map[string]bool{}
	var out []time.Time
	for _, f := range filters {
		if !seen[key(f.Date)] {
			out = append(out, f.Date)
			seen[key(f.Date)] = true
		}
	}
	return out
}

func reportDates(f domain.Filter) []time.Time {
	p := append(points(f), domain.Previous(f))
	return allDates(p...)
}

func nimDate(asOf time.Time, month int) time.Time {
	// Carbon's setMonth overflows days absent from the target month (e.g. 31 February).
	return time.Date(asOf.Year(), time.Month(month), asOf.Day(), 0, 0, 0, 0, time.UTC)
}

type snapshot struct {
	savings  map[string]fundingPosition
	deposits map[string]fundingPosition
	loans    map[string]loanPosition
	balance  map[string]balancePosition
}

func (s *DashboardService) load(ctx context.Context, f domain.Filter, kinds string) (snapshot, error) {
	var x snapshot
	days := reportDates(f)
	err := s.repo.read(ctx, func(q reader) (err error) {
		if strings.Contains(kinds, "s") {
			x.savings, err = q.SavingsPosition(ctx, days, f.Branch)
			if err != nil {
				return err
			}
		}
		if strings.Contains(kinds, "d") {
			x.deposits, err = q.DepositPosition(ctx, days, f.Branch)
			if err != nil {
				return err
			}
		}
		if strings.Contains(kinds, "l") {
			x.loans, err = q.LoanPosition(ctx, days, f.Branch, f.Mode)
			if err != nil {
				return err
			}
		}
		if strings.Contains(kinds, "b") {
			for _, target := range []domain.Filter{f, domain.Previous(f)} {
				for month := 1; month <= int(target.Date.Month()); month++ {
					days = append(days, nimDate(target.Date, month))
				}
			}
			unique := map[string]bool{}
			var balanceDays []time.Time
			for _, day := range days {
				if !unique[key(day)] {
					balanceDays = append(balanceDays, day)
					unique[key(day)] = true
				}
			}
			x.balance, err = q.BalanceSheetPosition(ctx, balanceDays, f.Branch)
		}
		return err
	})
	return x, err
}

func funding(p fundingPosition, category, metric string) int64 {
	switch metric {
	case "noa":
		if category == "abp" {
			return p.ABPNOA
		}
		if category == "dpk" {
			return p.NOA - p.ABPNOA
		}
		return p.NOA
	default:
		if category == "abp" {
			return p.ABP
		}
		if category == "dpk" {
			return p.Total - p.ABP
		}
		return p.Total
	}
}

func metric(label, unit, domainName, category, metricKey string, value, previous int64) domain.Metric {
	return domain.Metric{Label: label, Unit: unit, Domain: domainName, Category: category, Key: metricKey, Value: value, Previous: previous}
}

func (x snapshot) fundingMetric(f domain.Filter, table map[string]fundingPosition, kind, category, field, label, unit string) domain.Metric {
	return metric(label, unit, kind, category, field,
		funding(table[key(f.Date)], category, field), funding(table[key(domain.Previous(f).Date)], category, field))
}

func trend(f domain.Filter, name string, value func(domain.Filter) int64) domain.Series {
	series := domain.Series{Name: name}
	for _, p := range points(f) {
		series.Points = append(series.Points, domain.Point{Label: label(p), Value: value(p)})
	}
	return series
}

func percent(n, d int64) int64 {
	if d == 0 {
		return 0
	}
	return int64(math.Round(float64(n) / float64(d) * 10000))
}

func (x snapshot) financial(f domain.Filter) map[string]int64 {
	b := x.balance[key(f.Date)]
	if b == nil {
		return nil
	}
	get := func(coas ...string) int64 {
		var v int64
		for _, c := range coas {
			v += b[c]
		}
		return v
	}
	loan := get("121", "122")
	deposit := get("221", "2312200", "2312201") - get("2212111", "2212116", "2212199")
	liquid := get("100", "111", "112") // Owner-approved blocked ABA assumption: zero.
	liabilities := get("211", "212", "213", "219", "2011008", "2011001", "2011004", "2011005", "2011006", "2011007", "208", "221", "2312200", "2312201")
	income := get("401", "402", "403", "410")
	expense := get("501", "502", "511")
	var productive int64
	completeNIM := true
	for month := 1; month <= int(f.Date.Month()); month++ {
		m := nimDate(f.Date, month)
		v := x.balance[key(m)]
		if v == nil {
			completeNIM = false
			break
		}
		productive += v["110"] + v["121"]
	}
	result := map[string]int64{
		"Total Aset": get("1"), "Laba Sebelum Pajak": get("323", "558"),
		"BOPO": percent(get("5")-get("558"), get("4")),
		"LDR":  percent(loan, deposit), "Cash Ratio": percent(liquid, liabilities),
	}
	if completeNIM && productive != 0 {
		result["NIM"] = percent((income-expense)*12, productive)
	}
	if p, ok := x.loans[key(f.Date)]; ok {
		result["NPL"] = percent(p.Bad, p.Outstanding)
	}
	return result
}

func (x snapshot) ratio(f domain.Filter, name string) domain.Metric {
	return metric(name, "percent", "", "", "", x.financial(f)[name], x.financial(domain.Previous(f))[name])
}

func (s *DashboardService) GetOverview(ctx context.Context, f domain.Filter) (domain.Dashboard, error) {
	x, err := s.load(ctx, f, "sdlb")
	if err != nil {
		return domain.Dashboard{}, err
	}
	d := domain.Dashboard{Title: "Overview", Subtitle: "Ringkasan posisi dan kinerja bank"}
	if x.savings[key(f.Date)] == (fundingPosition{}) && x.deposits[key(f.Date)] == (fundingPosition{}) && x.loans[key(f.Date)] == (loanPosition{}) {
		d.Empty = true
		return d, nil
	}
	loan := func(p domain.Filter) int64 { return x.loans[key(p.Date)].Outstanding }
	d.Metrics = []domain.Metric{
		x.fundingMetric(f, x.savings, "tabungan", "all", "balance", "Total Tabungan", "rupiah"),
		x.fundingMetric(f, x.deposits, "deposito", "all", "balance", "Total Deposito", "rupiah"),
		metric("Outstanding Kredit", "rupiah", "kredit", "all", "bade", loan(f), loan(domain.Previous(f))),
	}
	for _, name := range []string{"LDR", "NPL", "Cash Ratio", "NIM"} {
		if _, ok := x.financial(f)[name]; ok {
			d.Secondary = append(d.Secondary, x.ratio(f, name))
		}
	}
	d.Series = []domain.Series{
		trend(f, "Tabungan", func(p domain.Filter) int64 { return x.savings[key(p.Date)].Total }),
		trend(f, "Deposito", func(p domain.Filter) int64 { return x.deposits[key(p.Date)].Total }),
		trend(f, "Organik", loan), trend(f, "Channeling", func(domain.Filter) int64 { return 0 }),
	}
	return d, nil
}

func (s *DashboardService) GetSavings(ctx context.Context, f domain.Filter, category string) (domain.Dashboard, error) {
	if category != "dpk" && category != "abp" {
		return domain.Dashboard{}, errors.New("kategori tabungan tidak valid")
	}
	x, err := s.load(ctx, f, "s")
	if err != nil {
		return domain.Dashboard{}, err
	}
	d := domain.Dashboard{Title: "Tabungan", Subtitle: strings.ToUpper(category)}
	if _, ok := x.savings[key(f.Date)]; !ok {
		d.Empty = true
		return d, nil
	}
	bal := x.fundingMetric(f, x.savings, "tabungan", category, "balance", "Saldo Tabungan", "rupiah")
	noa := x.fundingMetric(f, x.savings, "tabungan", category, "noa", "Jumlah Rekening", "count")
	avg := metric("Rata-rata Saldo", "rupiah", "", "", "", 0, 0)
	if noa.Value != 0 {
		avg.Value = bal.Value / noa.Value
	}
	if noa.Previous != 0 {
		avg.Previous = bal.Previous / noa.Previous
	}
	d.Metrics = []domain.Metric{bal, noa, avg}
	d.Series = []domain.Series{trend(f, "Saldo", func(p domain.Filter) int64 { return funding(x.savings[key(p.Date)], category, "balance") })}
	return d, nil
}

func (s *DashboardService) GetDeposits(ctx context.Context, f domain.Filter, category string) (domain.Dashboard, error) {
	if category != "dpk" && category != "abp" {
		return domain.Dashboard{}, errors.New("kategori deposito tidak valid")
	}
	x, err := s.load(ctx, f, "d")
	if err != nil {
		return domain.Dashboard{}, err
	}
	d := domain.Dashboard{Title: "Deposito", Subtitle: strings.ToUpper(category)}
	if _, ok := x.deposits[key(f.Date)]; !ok {
		d.Empty = true
		return d, nil
	}
	d.Metrics = []domain.Metric{
		x.fundingMetric(f, x.deposits, "deposito", category, "balance", "Nominal Deposito", "rupiah"),
		x.fundingMetric(f, x.deposits, "deposito", category, "noa", "Jumlah Rekening", "count"),
	}
	d.Series = []domain.Series{trend(f, "Nominal", func(p domain.Filter) int64 { return funding(x.deposits[key(p.Date)], category, "balance") })}
	return d, nil
}

func (s *DashboardService) GetLoans(ctx context.Context, f domain.Filter) (domain.Dashboard, error) {
	x, err := s.load(ctx, f, "l")
	if err != nil {
		return domain.Dashboard{}, err
	}
	d := domain.Dashboard{Title: "Kredit", Subtitle: "Channeling dan Organik"}
	if _, ok := x.loans[key(f.Date)]; !ok {
		d.Empty = true
		return d, nil
	}
	for _, cat := range []string{"channeling", "organik"} {
		value := func(p domain.Filter) loanPosition {
			if cat == "channeling" {
				return loanPosition{}
			}
			return x.loans[key(p.Date)]
		}
		current, previous := value(f), value(domain.Previous(f))
		name := strings.ToUpper(cat)
		d.Groups = append(d.Groups, domain.Group{Title: name, Metrics: []domain.Metric{
			metric("Booking", "rupiah", "kredit", cat, "booking", current.Booking, previous.Booking),
			metric("BADE", "rupiah", "kredit", cat, "bade", current.Outstanding, previous.Outstanding),
		}})
		d.Series = append(d.Series,
			trend(f, name+" Booking", func(p domain.Filter) int64 { return value(p).Booking }),
			trend(f, name+" BADE", func(p domain.Filter) int64 { return value(p).Outstanding }))
	}
	return d, nil
}

func (s *DashboardService) GetFinancialPerformance(ctx context.Context, f domain.Filter) (domain.Dashboard, error) {
	x, err := s.load(ctx, f, "bl")
	if err != nil {
		return domain.Dashboard{}, err
	}
	d := domain.Dashboard{Title: "Kinerja Keuangan", Subtitle: "Rasio dan nominal"}
	if x.balance[key(f.Date)] == nil {
		d.Empty = true
		return d, nil
	}
	r := domain.Group{Title: "Rasio"}
	for _, name := range []string{"NIM", "LDR", "BOPO", "Cash Ratio", "NPL"} {
		if _, ok := x.financial(f)[name]; ok {
			r.Metrics = append(r.Metrics, x.ratio(f, name))
		}
	}
	current, previous := x.financial(f), x.financial(domain.Previous(f))
	n := domain.Group{Title: "Nominal Keuangan", Metrics: []domain.Metric{
		metric("Total Aset", "rupiah", "", "", "", current["Total Aset"], previous["Total Aset"]),
		metric("Laba Sebelum Pajak", "rupiah", "", "", "", current["Laba Sebelum Pajak"], previous["Laba Sebelum Pajak"]),
	}}
	d.Groups = []domain.Group{r, n}
	d.Series = []domain.Series{
		trend(f, "Aset", func(p domain.Filter) int64 { return x.balance[key(p.Date)]["1"] }),
		trend(f, "Laba", func(p domain.Filter) int64 { b := x.balance[key(p.Date)]; return b["323"] + b["558"] }),
	}
	return d, nil
}

func (s *DashboardService) GetDepositMaturities(ctx context.Context, f domain.Filter) (domain.Dashboard, error) {
	d := domain.Dashboard{Title: "Deposito", Subtitle: "Jatuh Tempo"}
	var rows []maturityRow
	err := s.repo.read(ctx, func(q reader) (err error) { rows, err = q.Maturities(ctx, f.Date, f.Branch); return err })
	if err != nil {
		return domain.Dashboard{}, err
	}
	if len(rows) == 0 {
		d.Empty = true
		return d, nil
	}
	series := domain.Series{Name: "Nominal jatuh tempo"}
	for week := 0; week < 12; week++ {
		series.Points = append(series.Points, domain.Point{Label: fmt.Sprintf("Minggu %d", week+1)})
	}
	for _, b := range []struct {
		name, bucket string
		min, max     int
	}{{"≤ 7 hari", "0-7", 0, 7}, {"8–30 hari", "8-30", 8, 30}, {"> 30 hari", "31+", 31, 99999}} {
		var noa, amount int64
		for _, r := range rows {
			days := int(r.Due.Sub(f.Date).Hours() / 24)
			if days >= b.min && days <= b.max {
				noa++
				amount += r.AmountCents
			}
		}
		d.Groups = append(d.Groups, domain.Group{Title: b.name, Metrics: []domain.Metric{
			{Label: "NOA", Value: noa, Unit: "count", Domain: "deposito", Category: "jatuh-tempo", Key: "noa", Bucket: b.bucket},
			{Label: "Nominal", Value: int64(math.Round(float64(amount) / 100)), Unit: "rupiah", Domain: "deposito", Category: "jatuh-tempo", Key: "balance", Bucket: b.bucket},
		}})
	}
	for _, r := range rows {
		d.Maturities = append(d.Maturities, domain.Maturity{Name: r.Name, Account: r.Account, Branch: r.Branch, Due: r.Due, Amount: int64(math.Round(float64(r.AmountCents) / 100))})
		week := int(r.Due.Sub(f.Date).Hours()/24) / 7
		if week >= 0 && week < 12 {
			series.Points[week].Value += r.AmountCents
		}
	}
	for i := range series.Points {
		series.Points[i].Value = int64(math.Round(float64(series.Points[i].Value) / 100))
	}
	d.Series = []domain.Series{series}
	return d, nil
}
