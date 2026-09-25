package dwh

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/ibldzn/dashboard-roro-jongrang/internal/domain"
	"github.com/ibldzn/dashboard-roro-jongrang/internal/service/newsinergi"
)

// MetricStore writes only to APP_DBSTRING. Source repositories remain read-only.
type MetricStore struct{ db *sql.DB }

func NewMetricStore(db *sql.DB) *MetricStore { return &MetricStore{db: db} }

type metricRow struct {
	date, branch, name, source string
	value, snapshotID          int64
}
type previewRow struct {
	date, branch, source         string
	slot                         int
	name, account, accountBranch string
	due                          time.Time
	amount, id                   int64
}

var branches = []string{"001", "002", "003", "004", "005", "006", "007", "008", "ALL"}

func (m *MetricStore) put(ctx context.Context, tx *sql.Tx, rows []metricRow, from, to time.Time, source string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM dashboard_daily_metrics WHERE source=? AND metric_date BETWEEN ? AND ?`, source, from, to); err != nil {
		return err
	}
	now := time.Now().UTC()
	for start := 0; start < len(rows); start += 250 {
		end := min(start+250, len(rows))
		args := make([]any, 0, (end-start)*7)
		var query strings.Builder
		query.WriteString(`INSERT INTO dashboard_daily_metrics (metric_date,branch_code,metric_key,metric_value,source,source_snapshot_id,built_at) VALUES `)
		for i := start; i < end; i++ {
			if i > start {
				query.WriteByte(',')
			}
			query.WriteString("(?,?,?,?,?,?,?)")
			r := rows[i]
			var id any
			if r.snapshotID != 0 {
				id = r.snapshotID
			}
			args = append(args, r.date, r.branch, r.name, r.value, r.source, id, now)
		}
		if _, err := tx.ExecContext(ctx, query.String(), args...); err != nil {
			return err
		}
	}
	return nil
}

func (m *MetricStore) putPreview(ctx context.Context, tx *sql.Tx, rows []previewRow, from, to time.Time, source string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM dashboard_maturity_preview WHERE source=? AND metric_date BETWEEN ? AND ?`, source, from, to); err != nil {
		return err
	}
	for start := 0; start < len(rows); start += 250 {
		end := min(start+250, len(rows))
		var query strings.Builder
		query.WriteString(`INSERT INTO dashboard_maturity_preview (metric_date,branch_code,source,slot,customer_name,account_no,account_branch,maturity_date,amount,source_snapshot_id) VALUES `)
		args := make([]any, 0, (end-start)*10)
		for i := start; i < end; i++ {
			if i > start {
				query.WriteByte(',')
			}
			query.WriteString("(?,?,?,?,?,?,?,?,?,?)")
			r := rows[i]
			var id any
			if r.id != 0 {
				id = r.id
			}
			args = append(args, r.date, r.branch, r.source, r.slot, r.name, r.account, r.accountBranch, r.due, r.amount, id)
		}
		if _, err := tx.ExecContext(ctx, query.String(), args...); err != nil {
			return err
		}
	}
	return nil
}

func (m *MetricStore) preview(ctx context.Context, day time.Time, branch, source string) ([]domain.Maturity, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT customer_name,account_no,account_branch,maturity_date,amount FROM dashboard_maturity_preview WHERE metric_date=? AND branch_code=? AND source=? ORDER BY slot`, day, branch, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Maturity
	for rows.Next() {
		var x domain.Maturity
		if err = rows.Scan(&x.Name, &x.Account, &x.Branch, &x.Due, &x.Amount); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (m *MetricStore) read(ctx context.Context, branch string, from, to time.Time, sourceFor func(time.Time) string) (map[string]map[string]int64, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT metric_date,metric_key,metric_value,source FROM dashboard_daily_metrics WHERE branch_code=? AND metric_date BETWEEN ? AND ? AND source IN ('dwh','realtime') ORDER BY metric_date`, branch, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMetrics(rows, sourceFor)
}

func (m *MetricStore) readDates(ctx context.Context, branch string, days []time.Time, sourceFor func(time.Time) string) (map[string]map[string]int64, error) {
	mark, args := dates(days)
	args = append([]any{branch}, args...)
	rows, err := m.db.QueryContext(ctx, `SELECT metric_date,metric_key,metric_value,source FROM dashboard_daily_metrics WHERE branch_code=? AND metric_date IN (`+mark+`) AND source IN ('dwh','realtime') ORDER BY metric_date`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMetrics(rows, sourceFor)
}

func (m *MetricStore) readBooking(ctx context.Context, branch string, from, to time.Time, sourceFor func(time.Time) string) (map[string]map[string]int64, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT metric_date,metric_key,metric_value,source FROM dashboard_daily_metrics WHERE branch_code=? AND metric_date BETWEEN ? AND ? AND metric_key IN ('loan_present','loan_organic_booking') AND source IN ('dwh','realtime') ORDER BY metric_date`, branch, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMetrics(rows, sourceFor)
}

func scanMetrics(rows *sql.Rows, sourceFor func(time.Time) string) (map[string]map[string]int64, error) {
	out := map[string]map[string]int64{}
	var err error
	for rows.Next() {
		var day time.Time
		var name, source string
		var value int64
		if err = rows.Scan(&day, &name, &value, &source); err != nil {
			return nil, err
		}
		if source != sourceFor(day) {
			continue
		}
		k := key(day)
		if out[k] == nil {
			out[k] = map[string]int64{}
		}
		out[k][name] = value
	}
	return out, rows.Err()
}

func (m *MetricStore) channeling(ctx context.Context, f domain.Filter) (map[string]newsinergi.Position, []domain.Filter, error) {
	ps := append(points(f), domain.Previous(f))
	first := domain.PeriodStart(ps[0])
	for _, p := range ps {
		if d := domain.PeriodStart(p); d.Before(first) {
			first = d
		}
	}
	rows, err := m.db.QueryContext(ctx, `SELECT metric_date,metric_key,metric_value FROM dashboard_daily_metrics WHERE source='newsinergi' AND branch_code=? AND metric_date BETWEEN ? AND ? ORDER BY metric_date`, f.Branch, first, f.Date)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	vals := map[string]map[string]int64{}
	for rows.Next() {
		var d time.Time
		var name string
		var v int64
		if err = rows.Scan(&d, &name, &v); err != nil {
			return nil, nil, err
		}
		k := key(d)
		if vals[k] == nil {
			vals[k] = map[string]int64{}
		}
		vals[k][name] = v
	}
	if err = rows.Err(); err != nil {
		return nil, nil, err
	}
	out := map[string]newsinergi.Position{}
	var missing []domain.Filter
	seen := map[string]bool{}
	for _, p := range ps {
		if seen[key(p.Date)] {
			continue
		}
		seen[key(p.Date)] = true
		v := vals[key(p.Date)]
		if _, ok := v["channeling_present"]; !ok {
			missing = append(missing, p)
			continue
		}
		var booking int64
		complete := true
		for d := domain.PeriodStart(p); !d.After(p.Date); d = d.AddDate(0, 0, 1) {
			row := vals[key(d)]
			if _, ok := row["channeling_present"]; !ok {
				complete = false
				break
			}
			booking += row["loan_channeling_booking"]
		}
		if !complete {
			missing = append(missing, p)
			continue
		}
		out[key(p.Date)] = newsinergi.Position{Booking: booking, Plafond: v["loan_channeling_plafond"], Exists: v["channeling_exists"] != 0}
	}
	return out, missing, nil
}

func (s *DashboardService) cachedMaturities(ctx context.Context, f domain.Filter) (domain.Dashboard, bool, error) {
	values, err := s.metrics.read(ctx, f.Branch, f.Date, f.Date, func(day time.Time) string {
		if s.sourceFor(day) == s.realtime && s.realtime != nil {
			return "realtime"
		}
		return "dwh"
	})
	if err != nil {
		return domain.Dashboard{}, false, err
	}
	v := values[key(f.Date)]
	if _, ok := v["maturity_present"]; !ok {
		return domain.Dashboard{}, false, nil
	}
	d := maturityDashboard(v)
	if d.Empty {
		return d, true, nil
	}
	source := "dwh"
	if s.sourceFor(f.Date) == s.realtime && s.realtime != nil {
		source = "realtime"
	}
	d.Maturities, err = s.metrics.preview(ctx, f.Date, f.Branch, source)
	if err != nil {
		return domain.Dashboard{}, false, err
	}
	return d, true, nil
}

func maturityDashboard(v map[string]int64) domain.Dashboard {
	d := domain.Dashboard{Title: "Deposito", Subtitle: "Jatuh Tempo"}
	for _, b := range []struct{ name, suffix, bucket string }{{"≤ 7 hari", "7d", "0-7"}, {"8–30 hari", "30d", "8-30"}, {"> 30 hari", "gt30d", "31+"}} {
		noa, amount := v["deposit_due_"+b.suffix+"_noa"], v["deposit_due_"+b.suffix+"_nominal"]
		d.Groups = append(d.Groups, domain.Group{Title: b.name, Metrics: []domain.Metric{{Label: "NOA", Value: noa, Unit: "count", Domain: "deposito", Category: "jatuh-tempo", Key: "noa", Bucket: b.bucket}, {Label: "Nominal", Value: amount, Unit: "rupiah", Domain: "deposito", Category: "jatuh-tempo", Key: "balance", Bucket: b.bucket}}})
	}
	series := domain.Series{Name: "Nominal jatuh tempo"}
	for week := 0; week < 12; week++ {
		series.Points = append(series.Points, domain.Point{Label: fmt.Sprintf("Minggu %d", week+1), Value: v[fmt.Sprintf("deposit_due_week_%d", week)]})
	}
	d.Series = []domain.Series{series}
	if v["deposit_due_7d_noa"]+v["deposit_due_30d_noa"]+v["deposit_due_gt30d_noa"] == 0 {
		d.Empty = true
	}
	return d
}

func (s *DashboardService) cachedSnapshot(ctx context.Context, f domain.Filter, kinds string) (snapshot, map[string][]time.Time, error) {
	requested := reportDates(f)
	last := requested[0]
	for _, d := range requested {
		if d.After(last) {
			last = d
		}
	}
	firstBooking := last
	if strings.Contains(kinds, "l") {
		for _, p := range append(points(f), domain.Previous(f)) {
			if d := domain.PeriodStart(p); d.Before(firstBooking) {
				firstBooking = d
			}
		}
	}
	sourceFor := func(day time.Time) string {
		if s.sourceFor(day) == s.realtime && s.realtime != nil {
			return "realtime"
		}
		return "dwh"
	}
	vals, err := s.metrics.readDates(ctx, f.Branch, requested, sourceFor)
	if err != nil {
		return snapshot{}, nil, err
	}
	if strings.Contains(kinds, "l") {
		booking, e := s.metrics.readBooking(ctx, f.Branch, firstBooking, last, sourceFor)
		if e != nil {
			return snapshot{}, nil, e
		}
		for date, metrics := range booking {
			if vals[date] == nil {
				vals[date] = map[string]int64{}
			}
			for name, value := range metrics {
				vals[date][name] = value
			}
		}
	}
	x := snapshot{savings: map[string]fundingPosition{}, deposits: map[string]fundingPosition{}, loans: map[string]loanPosition{}, balance: map[string]balancePosition{}, cachedFinancial: map[string]map[string]int64{}}
	missing := map[string][]time.Time{}
	for _, d := range requested {
		v := vals[key(d)]
		for _, kind := range kinds {
			marker := map[rune]string{'s': "savings_present", 'd': "deposit_present", 'l': "loan_present", 'b': "balance_present"}[kind]
			if _, ok := v[marker]; !ok {
				missing[string(kind)] = append(missing[string(kind)], d)
			}
		}
	}
	for _, d := range requested {
		v := vals[key(d)]
		k := key(d)
		if v["savings_present"] != 0 {
			x.savings[k] = fundingPosition{v["savings_total"], v["savings_abp"], v["savings_noa"], v["savings_abp_noa"]}
		}
		if v["deposit_present"] != 0 {
			x.deposits[k] = fundingPosition{v["deposit_total"], v["deposit_abp"], v["deposit_noa"], v["deposit_abp_noa"]}
		}
		if v["loan_present"] != 0 {
			x.loans[k] = loanPosition{Outstanding: v["loan_organic_bade"], Bad: v["npl_bad_outstanding"]}
		}
		if v["balance_present"] != 0 {
			x.balance[k] = balancePosition{"1": v["asset"], "323": v["profit_before_tax"]}
			x.cachedFinancial[k] = map[string]int64{}
			for metric, name := range map[string]string{"asset": "Aset", "profit_before_tax": "Laba Sebelum Pajak", "nim": "NIM", "bopo": "BOPO", "ldr": "LDR", "cash_ratio": "Cash Ratio", "npl_percentage": "NPL"} {
				if value, exists := v[metric]; exists {
					x.cachedFinancial[k][name] = value
				}
			}
		}
	}
	// Booking is a flow. Sum daily local rows over each requested reporting period.
	if strings.Contains(kinds, "l") {
		for _, p := range append(points(f), domain.Previous(f)) {
			k := key(p.Date)
			if _, exists := vals[k]["loan_present"]; !exists {
				continue
			}
			var booking int64
			complete := true
			for d := domain.PeriodStart(p); !d.After(p.Date); d = d.AddDate(0, 0, 1) {
				v := vals[key(d)]
				if _, ok := v["loan_present"]; !ok {
					complete = false
					break
				}
				booking += v["loan_organic_booking"]
			}
			if complete {
				loan := x.loans[k]
				loan.Booking = booking
				if vals[k]["loan_present"] != 0 {
					x.loans[k] = loan
				}
			} else {
				missing["l"] = append(missing["l"], p.Date)
			}
		}
	}
	return x, missing, nil
}

func (s *DashboardService) SetMetricStore(m *MetricStore) { s.metrics = m }

func (m *MetricStore) LatestDWH(ctx context.Context) (time.Time, error) {
	var d sql.NullTime
	err := m.db.QueryRowContext(ctx, `SELECT MAX(date_to) FROM dashboard_materialization_runs WHERE source='dwh' AND status='complete'`).Scan(&d)
	if err != nil {
		return time.Time{}, err
	}
	if !d.Valid {
		return time.Time{}, nil
	}
	return d.Time, nil
}

func (m *MetricStore) Materialize(ctx context.Context, repo *Repository, channel *newsinergi.Repository, from, to time.Time, force bool) error {
	if from.IsZero() || to.Before(from) {
		return errors.New("invalid materialization date range")
	}
	// Bound each source query and transaction to one month, independent of CLI range length.
	for start := from; !start.After(to); {
		end := time.Date(start.Year(), start.Month()+1, 0, 0, 0, 0, 0, time.UTC)
		if end.After(to) {
			end = to
		}
		ranges := [][2]time.Time{{start, end}}
		if !force {
			var err error
			ranges, err = m.missingRanges(ctx, start, end)
			if err != nil {
				return err
			}
		}
		for _, r := range ranges {
			if err := m.materializeChunk(ctx, repo, channel, r[0], r[1]); err != nil {
				return err
			}
			log.Printf("dashboard materialized %s to %s", key(r[0]), key(r[1]))
		}
		start = end.AddDate(0, 0, 1)
	}
	return nil
}

func (m *MetricStore) missingRanges(ctx context.Context, from, to time.Time) ([][2]time.Time, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT metric_date FROM dashboard_daily_metrics WHERE source='dwh' AND branch_code='ALL' AND metric_key='savings_present' AND metric_date BETWEEN ? AND ?`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	covered := map[string]bool{}
	for rows.Next() {
		var day time.Time
		if err = rows.Scan(&day); err != nil {
			return nil, err
		}
		covered[key(day)] = true
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	var out [][2]time.Time
	var start time.Time
	for day := from; !day.After(to.AddDate(0, 0, 1)); day = day.AddDate(0, 0, 1) {
		if day.After(to) || covered[key(day)] {
			if !start.IsZero() {
				out = append(out, [2]time.Time{start, day.AddDate(0, 0, -1)})
				start = time.Time{}
			}
		} else if start.IsZero() {
			start = day
		}
	}
	return out, nil
}

func (m *MetricStore) materializeChunk(ctx context.Context, repo *Repository, channel *newsinergi.Repository, from, to time.Time) (err error) {
	now := time.Now().UTC()
	res, err := m.db.ExecContext(ctx, `INSERT INTO dashboard_materialization_runs (source,date_from,date_to,started_at,status) VALUES ('dwh',?,?,?,'running')`, from, to, now)
	if err != nil {
		return err
	}
	runID, _ := res.LastInsertId()
	defer func() {
		status := "complete"
		message := ""
		if err != nil {
			status = "failed"
			message = err.Error()
			if len(message) > 500 {
				message = message[:500]
			}
		}
		_, e := m.db.ExecContext(context.Background(), `UPDATE dashboard_materialization_runs SET status=?,completed_at=?,error_message=? WHERE id=?`, status, time.Now().UTC(), message, runID)
		if err == nil {
			err = e
		}
	}()
	var days []time.Time
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		days = append(days, d)
	}
	rows := make([]metricRow, 0, len(days)*len(branches)*30)
	// NIM uses the same monthly Balance Sheet samples as the request fallback.
	samples := map[string]time.Time{}
	for _, d := range days {
		samples[key(d)] = d
		for month := 1; month <= int(d.Month()); month++ {
			sample := nimDate(d, month)
			samples[key(sample)] = sample
		}
	}
	balanceDays := make([]time.Time, 0, len(samples))
	for _, d := range samples {
		balanceDays = append(balanceDays, d)
	}
	sort.Slice(balanceDays, func(i, j int) bool { return balanceDays[i].Before(balanceDays[j]) })
	var savings, deposits map[string]map[string]fundingPosition
	var loans map[string]map[string]loanPosition
	var balance map[string]map[string]balancePosition
	err = repo.read(ctx, func(q reader) (e error) {
		if savings, e = q.groupedFunding(ctx, days, q.tables.savings, "credit_balance", "branch", savingsABP, true); e != nil {
			return e
		}
		if deposits, e = q.groupedFunding(ctx, days, q.tables.deposits, "nominal", "branch_code", depositABP, false); e != nil {
			return e
		}
		if loans, e = q.groupedLoans(ctx, days); e != nil {
			return e
		}
		balance, e = q.groupedBalance(ctx, balanceDays)
		return e
	})
	if err != nil {
		return err
	}
	for _, branch := range branches {
		x := snapshot{savings: savings[branch], deposits: deposits[branch], loans: loans[branch], balance: balance[branch]}
		for _, d := range days {
			rows = append(rows, metricRows(x, d, branch, "dwh", 0)...)
		}
	}
	var maturities []metricRow
	err = repo.read(ctx, func(q reader) (e error) { maturities, e = maturityMetrics(ctx, q, days, "dwh", 0); return e })
	if err != nil {
		return err
	}
	rows = append(rows, maturities...)
	var previews []previewRow
	err = repo.read(ctx, func(q reader) (e error) { previews, e = maturityPreviews(ctx, q, days, "dwh", 0); return e })
	if err != nil {
		return err
	}
	var channelRows []metricRow
	if channel != nil {
		channelRows, err = materializeChanneling(ctx, channel, from, to)
		if err != nil {
			return err
		}
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = m.put(ctx, tx, rows, from, to, "dwh"); err != nil {
		return err
	}
	if err = m.putPreview(ctx, tx, previews, from, to, "dwh"); err != nil {
		return err
	}
	if channel != nil {
		if err = m.put(ctx, tx, channelRows, from, to, "newsinergi"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func metricRows(x snapshot, d time.Time, branch, source string, id int64) []metricRow {
	day := key(d)
	out := []metricRow{}
	add := func(name string, value int64) { out = append(out, metricRow{day, branch, name, source, value, id}) }
	s, ok := x.savings[day]
	add("savings_present", boolInt(ok))
	if ok {
		add("savings_total", s.Total)
		add("savings_abp", s.ABP)
		add("savings_dpk", s.Total-s.ABP)
		add("savings_noa", s.NOA)
		add("savings_abp_noa", s.ABPNOA)
	}
	p, ok := x.deposits[day]
	add("deposit_present", boolInt(ok))
	if ok {
		add("deposit_total", p.Total)
		add("deposit_abp", p.ABP)
		add("deposit_dpk", p.Total-p.ABP)
		add("deposit_noa", p.NOA)
		add("deposit_abp_noa", p.ABPNOA)
	}
	l, ok := x.loans[day]
	add("loan_present", boolInt(ok))
	if ok {
		add("loan_organic_bade", l.Outstanding)
		add("loan_organic_booking", l.Booking)
		add("npl_bad_outstanding", l.Bad)
		add("npl_total_outstanding", l.Outstanding)
		add("npl_percentage", percent(l.Bad, l.Outstanding))
	}
	b := x.balance[day]
	add("balance_present", boolInt(b != nil))
	if b != nil {
		f := domain.Filter{Date: d, Mode: "daily", Branch: branch}
		for name, value := range x.financial(f) {
			metric := map[string]string{"Aset": "asset", "Laba Sebelum Pajak": "profit_before_tax", "NIM": "nim", "BOPO": "bopo", "LDR": "ldr", "Cash Ratio": "cash_ratio"}[name]
			if metric != "" {
				add(metric, value)
			}
		}
		sum := func(coas ...string) int64 {
			var v int64
			for _, c := range coas {
				v += b[c]
			}
			return v
		}
		add("ldr_loans", sum("121", "122"))
		add("ldr_deposits", sum("221", "2312200", "2312201")-sum("2212111", "2212116", "2212199"))
		add("bopo_expense", b["5"]-b["558"])
		add("bopo_income", b["4"])
		add("cash_liquid_assets", sum("100", "111", "112"))
		add("cash_current_liabilities", sum("211", "212", "213", "219", "2011008", "2011001", "2011004", "2011005", "2011006", "2011007", "208", "221", "2312200", "2312201"))
	}
	return out
}

func (m *MetricStore) MaterializeRealtime(ctx context.Context, tx *sql.Tx, id int64, day time.Time, repo *Repository, channel *newsinergi.Repository) error {
	rows := []metricRow{}
	snapshotTables := tables{fmt.Sprintf("(SELECT * FROM dashboard_rt_savings WHERE snapshot_id=%d) AS rt_savings", id), fmt.Sprintf("(SELECT * FROM dashboard_rt_time_deposits WHERE snapshot_id=%d) AS rt_deposits", id), fmt.Sprintf("(SELECT * FROM dashboard_rt_loans WHERE snapshot_id=%d) AS rt_loans", id), fmt.Sprintf("(SELECT * FROM dashboard_rt_balance_sheet WHERE snapshot_id=%d) AS rt_balance", id)}
	for _, branch := range branches {
		q := reader{tx: tx, tables: snapshotTables}
		x := snapshot{}
		var err error
		if x.savings, err = q.SavingsPosition(ctx, []time.Time{day}, branch); err != nil {
			return err
		}
		if x.deposits, err = q.DepositPosition(ctx, []time.Time{day}, branch); err != nil {
			return err
		}
		if x.loans, err = q.LoanPosition(ctx, []time.Time{day}, branch, "daily"); err != nil {
			return err
		}
		if x.balance, err = q.BalanceSheetPosition(ctx, []time.Time{day}, branch); err != nil {
			return err
		}
		// Prior Balance Sheet samples remain historical; today comes from published candidate.
		var samples []time.Time
		for month := 1; month <= int(day.Month()); month++ {
			d := nimDate(day, month)
			if !d.Equal(day) {
				samples = append(samples, d)
			}
		}
		if len(samples) > 0 {
			prior, e := loadFrom(ctx, repo, branch, "daily", samples, "b")
			if e != nil {
				return e
			}
			for k, v := range prior.balance {
				x.balance[k] = v
			}
		}
		rows = append(rows, metricRows(x, day, branch, "realtime", id)...)
	}
	maturity, err := maturityMetrics(ctx, reader{tx: tx, tables: snapshotTables}, []time.Time{day}, "realtime", id)
	if err != nil {
		return err
	}
	rows = append(rows, maturity...)
	previews, err := maturityPreviews(ctx, reader{tx: tx, tables: snapshotTables}, []time.Time{day}, "realtime", id)
	if err != nil {
		return err
	}
	if err = m.put(ctx, tx, rows, day, day, "realtime"); err != nil {
		return err
	}
	if err = m.putPreview(ctx, tx, previews, day, day, "realtime"); err != nil {
		return err
	}
	if channel != nil {
		channelRows, e := materializeChanneling(ctx, channel, day, day)
		if e != nil {
			return e
		}
		if e = m.put(ctx, tx, channelRows, day, day, "newsinergi"); e != nil {
			return e
		}
	}
	return nil
}

func maturityMetrics(ctx context.Context, q reader, days []time.Time, source string, id int64) ([]metricRow, error) {
	mark, args := dates(days)
	difference := "DATEDIFF(maturity_date,as_of_date)"
	query := fmt.Sprintf(`SELECT as_of_date,LEFT(branch_code,3),
		CASE WHEN %s<=7 THEN 0 WHEN %s<=30 THEN 1 ELSE 2 END,
		LEAST(FLOOR(%s/7),12),COUNT(*),
		CAST(ROUND(COALESCE(SUM(ROUND(%s*100,0)),0),0) AS SIGNED)
		FROM %s WHERE as_of_date IN (%s) AND maturity_date>=as_of_date
		GROUP BY as_of_date,LEFT(branch_code,3),3,4`, difference, difference, difference, money("nominal"), q.tables.deposits, mark)
	result, err := q.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer result.Close()
	type aggregate struct{ count, cents int64 }
	values := map[string]map[string]aggregate{}
	for result.Next() {
		var d time.Time
		var branch string
		var bucket, week int
		var count, cents int64
		if err = result.Scan(&d, &branch, &bucket, &week, &count, &cents); err != nil {
			return nil, err
		}
		for _, b := range []string{branch, "ALL"} {
			k := key(d) + "/" + b
			if values[k] == nil {
				values[k] = map[string]aggregate{}
			}
			v := values[k][fmt.Sprintf("bucket_%d", bucket)]
			v.count += count
			v.cents += cents
			values[k][fmt.Sprintf("bucket_%d", bucket)] = v
			if week < 12 {
				v = values[k][fmt.Sprintf("week_%d", week)]
				v.cents += cents
				values[k][fmt.Sprintf("week_%d", week)] = v
			}
		}
	}
	if err = result.Err(); err != nil {
		return nil, err
	}
	var out []metricRow
	for _, d := range days {
		for _, branch := range branches {
			k := key(d)
			v := values[k+"/"+branch]
			out = append(out, metricRow{k, branch, "maturity_present", source, 1, id})
			for i := 0; i < 3; i++ {
				a := v[fmt.Sprintf("bucket_%d", i)]
				name := []string{"7d", "30d", "gt30d"}[i]
				out = append(out, metricRow{k, branch, "deposit_due_" + name + "_noa", source, a.count, id}, metricRow{k, branch, "deposit_due_" + name + "_nominal", source, int64(math.Round(float64(a.cents) / 100)), id})
			}
			for i := 0; i < 12; i++ {
				a := v[fmt.Sprintf("week_%d", i)]
				out = append(out, metricRow{k, branch, fmt.Sprintf("deposit_due_week_%d", i), source, int64(math.Round(float64(a.cents) / 100)), id})
			}
		}
	}
	return out, nil
}

func maturityPreviews(ctx context.Context, q reader, days []time.Time, source string, id int64) ([]previewRow, error) {
	mark, args := dates(days)
	var out []previewRow
	for _, consolidated := range []bool{false, true} {
		partition := "as_of_date,LEFT(branch_code,3)"
		branch := "LEFT(branch_code,3)"
		if consolidated {
			partition = "as_of_date"
			branch = "'ALL'"
		}
		query := fmt.Sprintf(`SELECT as_of_date,%s,rn,customer_name,account_no,LEFT(branch_code,3),DATE(maturity_date),
		 CAST(ROUND(%s,0) AS SIGNED) FROM (
		 SELECT as_of_date,branch_code,customer_name,account_no,maturity_date,nominal,
		 ROW_NUMBER() OVER (PARTITION BY %s ORDER BY maturity_date,account_no) rn
		 FROM %s WHERE as_of_date IN (%s) AND maturity_date>=as_of_date) ranked WHERE rn<=8`, branch, money("nominal"), partition, q.tables.deposits, mark)
		rows, err := q.tx.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var d time.Time
			var r previewRow
			var due any
			if err = rows.Scan(&d, &r.branch, &r.slot, &r.name, &r.account, &r.accountBranch, &due, &r.amount); err != nil {
				rows.Close()
				return nil, err
			}
			switch value := due.(type) {
			case time.Time:
				r.due = value
			case []byte:
				r.due, err = time.Parse("2006-01-02", string(value))
			case string:
				r.due, err = time.Parse("2006-01-02", value)
			default:
				err = fmt.Errorf("invalid maturity date %T", due)
			}
			if err != nil {
				rows.Close()
				return nil, err
			}
			r.date = key(d)
			r.source = source
			r.id = id
			out = append(out, r)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func materializeChanneling(ctx context.Context, repo *newsinergi.Repository, from, to time.Time) ([]metricRow, error) {
	entries, err := repo.Daily(ctx, to)
	if err != nil {
		return nil, err
	}
	type amount struct{ booking, count int64 }
	byBranch := map[string]map[string]amount{}
	for _, e := range entries {
		for _, branch := range []string{e.Branch, "ALL"} {
			if byBranch[branch] == nil {
				byBranch[branch] = map[string]amount{}
			}
			k := key(e.Date)
			v := byBranch[branch][k]
			v.booking += e.Booking
			v.count += e.Count
			byBranch[branch][k] = v
		}
	}
	out := []metricRow{}
	for _, branch := range branches {
		daily := byBranch[branch]
		var cumulative, count int64
		for k, v := range daily {
			d, e := time.Parse("2006-01-02", k)
			if e == nil && d.Before(from) {
				cumulative += v.booking
				count += v.count
			}
		}
		for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
			k := key(d)
			v := daily[k]
			cumulative += v.booking
			count += v.count
			out = append(out, metricRow{k, branch, "channeling_present", "newsinergi", 1, 0}, metricRow{k, branch, "channeling_exists", "newsinergi", boolInt(count > 0), 0}, metricRow{k, branch, "loan_channeling_booking", "newsinergi", v.booking, 0}, metricRow{k, branch, "loan_channeling_plafond", "newsinergi", cumulative, 0})
		}
	}
	return out, nil
}
func boolInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}
