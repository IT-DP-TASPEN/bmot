package dwh

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ibldzn/dashboard-roro-jongrang/internal/domain"
)

type testConnector struct {
	query func(string, []driver.NamedValue) (driver.Rows, error)
}

func (c testConnector) Connect(context.Context) (driver.Conn, error) {
	return &testConn{query: c.query}, nil
}
func (c testConnector) Driver() driver.Driver { return testDriver{} }

type testDriver struct{}

func (testDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type testConn struct {
	query func(string, []driver.NamedValue) (driver.Rows, error)
}

func (*testConn) Prepare(string) (driver.Stmt, error)                          { return nil, errors.New("unused") }
func (*testConn) Close() error                                                 { return nil }
func (*testConn) Begin() (driver.Tx, error)                                    { return testTx{}, nil }
func (*testConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) { return testTx{}, nil }
func (c *testConn) QueryContext(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	return c.query(q, args)
}

type testTx struct{}

func (testTx) Commit() error   { return nil }
func (testTx) Rollback() error { return nil }

type testRows struct {
	cols []string
	rows [][]driver.Value
	at   int
}

func (r *testRows) Columns() []string { return r.cols }
func (r *testRows) Close() error      { return nil }
func (r *testRows) Next(dst []driver.Value) error {
	if r.at == len(r.rows) {
		return io.EOF
	}
	copy(dst, r.rows[r.at])
	r.at++
	return nil
}
func testDB(fn func(string, []driver.NamedValue) (driver.Rows, error)) *sql.DB {
	return sql.OpenDB(testConnector{query: fn})
}
func testDate(s string) time.Time { d, _ := time.Parse("2006-01-02", s); return d }
func metricRowsForDay(d time.Time, name string, value int64) []driver.Value {
	return []driver.Value{d, name, value, "dwh"}
}

func TestCacheFirstAndMissingFallback(t *testing.T) {
	f := domain.Filter{Mode: "daily", Date: testDate("2026-09-24"), Branch: "001"}
	for _, cached := range []bool{true, false} {
		calls := 0
		localCalls := 0
		local := testDB(func(q string, _ []driver.NamedValue) (driver.Rows, error) {
			localCalls++
			if !strings.Contains(q, "dashboard_daily_metrics") || !strings.Contains(q, "metric_date IN (") {
				t.Fatalf("unexpected local query: %s", q)
			}
			r := &testRows{cols: []string{"metric_date", "metric_key", "metric_value", "source"}}
			if cached {
				for _, p := range points(f) {
					d := p.Date
					r.rows = append(r.rows, metricRowsForDay(d, "savings_present", 1), metricRowsForDay(d, "savings_total", 100), metricRowsForDay(d, "savings_abp", 20), metricRowsForDay(d, "savings_noa", 2), metricRowsForDay(d, "savings_abp_noa", 1))
				}
			}
			return r, nil
		})
		defer local.Close()
		source := testDB(func(q string, _ []driver.NamedValue) (driver.Rows, error) {
			calls++
			if !strings.Contains(q, savingsTable) {
				t.Fatalf("unexpected source query: %s", q)
			}
			return &testRows{cols: []string{"date", "total", "abp", "noa", "abpnoa"}, rows: [][]driver.Value{{f.Date, int64(100), int64(20), int64(2), int64(1)}}}, nil
		})
		defer source.Close()
		s := NewDashboardService(&Repository{db: source, tables: historicalTables})
		s.SetMetricStore(NewMetricStore(local))
		dashboard, err := s.GetSavings(context.Background(), f, "dpk")
		if err != nil {
			t.Fatal(err)
		}
		if dashboard.Empty || dashboard.Metrics[0].Value != 80 {
			t.Fatalf("cached=%v dashboard=%+v", cached, dashboard)
		}
		if cached && calls != 0 {
			t.Fatalf("cache hit queried DWH %d times", calls)
		}
		if localCalls != 1 {
			t.Fatalf("trend used %d local date queries", localCalls)
		}
		if !cached && calls == 0 {
			t.Fatal("cache miss did not query DWH")
		}
	}
}

func TestDailyBookingMonthlyAndYearly(t *testing.T) {
	local := testDB(func(q string, _ []driver.NamedValue) (driver.Rows, error) {
		if !strings.Contains(q, "dashboard_daily_metrics") {
			t.Fatalf("unexpected query: %s", q)
		}
		r := &testRows{cols: []string{"metric_date", "metric_key", "metric_value", "source"}}
		for d := testDate("2026-01-01"); !d.After(testDate("2026-09-24")); d = d.AddDate(0, 0, 1) {
			r.rows = append(r.rows, metricRowsForDay(d, "loan_present", 1), metricRowsForDay(d, "loan_organic_booking", 10), metricRowsForDay(d, "loan_organic_bade", 100))
		}
		return r, nil
	})
	defer local.Close()
	s := &DashboardService{metrics: NewMetricStore(local)}
	for _, tc := range []struct {
		mode string
		want int64
	}{{"daily", 10}, {"monthly", 240}, {"yearly", 2670}} {
		f := domain.Filter{Mode: tc.mode, Date: testDate("2026-09-24"), Branch: "001"}
		x, _, err := s.cachedSnapshot(context.Background(), f, "l")
		if err != nil {
			t.Fatal(err)
		}
		if got := x.loans[key(f.Date)].Booking; got != tc.want {
			t.Errorf("%s booking=%d want=%d", tc.mode, got, tc.want)
		}
	}
}

func TestMaterializedPositionAndConsolidatedRatio(t *testing.T) {
	day := testDate("2026-09-24")
	x := snapshot{savings: map[string]fundingPosition{key(day): {Total: 150, ABP: 50}}, loans: map[string]loanPosition{key(day): {Outstanding: 300, Bad: 30}}, balance: map[string]balancePosition{key(day): {"121": 200, "122": 100, "221": 150, "2312200": 150, "1": 1000}}}
	rows := metricRows(x, day, "ALL", "dwh", 0)
	got := map[string]int64{}
	for _, r := range rows {
		got[r.name] = r.value
	}
	for name, want := range map[string]int64{"savings_dpk": 100, "loan_organic_bade": 300, "npl_bad_outstanding": 30, "npl_percentage": 1000, "ldr": 10000, "asset": 1000} {
		if got[name] != want {
			t.Errorf("%s=%d want=%d", name, got[name], want)
		}
	}
}

func TestIncrementalMaterializationSkipsCompletedRange(t *testing.T) {
	day := testDate("2026-09-24")
	db := testDB(func(q string, _ []driver.NamedValue) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "MAX(date_to)"):
			return &testRows{cols: []string{"latest"}, rows: [][]driver.Value{{day}}}, nil
		case strings.Contains(q, "SELECT metric_date FROM dashboard_daily_metrics"):
			return &testRows{cols: []string{"metric_date"}, rows: [][]driver.Value{{day}}}, nil
		default:
			t.Fatalf("unexpected rebuild query: %s", q)
			return nil, nil
		}
	})
	defer db.Close()
	m := NewMetricStore(db)
	latest, err := m.LatestDWH(context.Background())
	if err != nil || !latest.Equal(day) {
		t.Fatalf("watermark=%v error=%v", latest, err)
	}
	if err = m.Materialize(context.Background(), nil, nil, day, day, false); err != nil {
		t.Fatal(err)
	}
}

func TestMaturityMetricsIncludeConsolidated(t *testing.T) {
	day := testDate("2026-09-24")
	db := testDB(func(q string, _ []driver.NamedValue) (driver.Rows, error) {
		if !strings.Contains(q, "DATEDIFF(maturity_date,as_of_date)") {
			t.Fatalf("unexpected query: %s", q)
		}
		return &testRows{cols: []string{"date", "branch", "bucket", "week", "count", "cents"}, rows: [][]driver.Value{{day, "001", int64(0), int64(1), int64(2), int64(1050)}, {day, "002", int64(0), int64(1), int64(1), int64(550)}}}, nil
	})
	defer db.Close()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	rows, err := maturityMetrics(context.Background(), reader{tx: tx, tables: historicalTables}, []time.Time{day}, "dwh", 0)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, r := range rows {
		if r.branch == "ALL" {
			got[r.name] = r.value
		}
	}
	if got["deposit_due_7d_noa"] != 3 || got["deposit_due_7d_nominal"] != 16 || got["deposit_due_week_1"] != 16 {
		t.Fatalf("consolidated maturity=%v", got)
	}
}

func TestChannelingLocalRange(t *testing.T) {
	f := domain.Filter{Mode: "daily", Date: testDate("2026-09-24"), Branch: "001"}
	db := testDB(func(q string, _ []driver.NamedValue) (driver.Rows, error) {
		if !strings.Contains(q, "source='newsinergi'") {
			t.Fatalf("unexpected query: %s", q)
		}
		r := &testRows{cols: []string{"date", "key", "value"}}
		for i, p := range points(f) {
			d := p.Date
			r.rows = append(r.rows, []driver.Value{d, "channeling_present", int64(1)}, []driver.Value{d, "channeling_exists", int64(1)}, []driver.Value{d, "loan_channeling_booking", int64(10)}, []driver.Value{d, "loan_channeling_plafond", int64((i + 1) * 10)})
		}
		return r, nil
	})
	defer db.Close()
	positions, missing, err := NewMetricStore(db).channeling(context.Background(), f)
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing=%v error=%v", missing, err)
	}
	if p := positions[key(f.Date)]; p.Booking != 10 || p.Plafond != 120 || !p.Exists {
		t.Fatalf("position=%+v", p)
	}
}

func TestNominativePaginationAndSingleUnfilteredCount(t *testing.T) {
	counts, pages := 0, 0
	db := testDB(func(q string, args []driver.NamedValue) (driver.Rows, error) {
		if strings.HasPrefix(q, "SELECT COUNT(*)") {
			counts++
			return &testRows{cols: []string{"count", "total"}, rows: [][]driver.Value{{int64(120), int64(120)}}}, nil
		}
		if strings.Contains(q, "LIMIT ? OFFSET ?") {
			pages++
			if len(args) < 2 || args[len(args)-2].Value != int64(50) || args[len(args)-1].Value != int64(50) {
				t.Fatalf("pagination args=%v", args)
			}
			return &testRows{cols: []string{"name", "account", "cif", "branch", "product", "collect", "amount", "outstanding", "due"}}, nil
		}
		t.Fatalf("unexpected query: %s", q)
		return nil, nil
	})
	defer db.Close()
	s := NewDashboardService(&Repository{db: db, tables: historicalTables})
	result, err := s.GetNominative(context.Background(), domain.NominativeFilter{Filter: domain.Filter{Date: testDate("2026-09-24"), Branch: "001"}, Domain: "tabungan", Category: "all", Metric: "balance", Page: 2})
	if err != nil {
		t.Fatal(err)
	}
	if counts != 1 || pages != 1 || result.Page != 2 || result.Pages != 3 {
		t.Fatalf("count queries=%d page queries=%d result=%+v", counts, pages, result)
	}
}

func TestGroupedBalanceConsolidatesComponents(t *testing.T) {
	day := testDate("2026-09-24")
	db := testDB(func(q string, _ []driver.NamedValue) (driver.Rows, error) {
		if !strings.Contains(q, "WITH ROLLUP") || !strings.Contains(q, "GROUP BY as_of_date,co_a_no,source_location_id") {
			t.Fatalf("not grouped: %s", q)
		}
		return &testRows{cols: []string{"date", "coa", "branch", "value"}, rows: [][]driver.Value{
			{day, "121", "001", int64(100)}, {day, "121", "002", int64(200)}, {day, "121", nil, int64(300)},
			{day, "221", "001", int64(200)}, {day, "221", "002", int64(100)}, {day, "221", nil, int64(300)},
			{nil, nil, nil, int64(900)},
		}}, nil
	})
	defer db.Close()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	grouped, err := (reader{tx: tx, tables: historicalTables}).groupedBalance(context.Background(), []time.Time{day})
	if err != nil {
		t.Fatal(err)
	}
	x := snapshot{balance: grouped["ALL"]}
	got := x.financial(domain.Filter{Mode: "daily", Date: day, Branch: "ALL"})["LDR"]
	if got != 10000 {
		t.Fatalf("consolidated LDR=%d want 10000", got)
	}
	if grouped["001"][key(day)]["121"] != 100 || grouped["002"][key(day)]["221"] != 100 {
		t.Fatalf("branch positions=%v", grouped)
	}
}

func TestMaturityPageUsesLocalSummaryAndPreview(t *testing.T) {
	day := testDate("2026-09-24")
	db := testDB(func(q string, _ []driver.NamedValue) (driver.Rows, error) {
		if strings.Contains(q, "dashboard_daily_metrics") {
			return &testRows{cols: []string{"date", "key", "value", "source"}, rows: [][]driver.Value{
				{day, "maturity_present", int64(1), "dwh"}, {day, "deposit_due_7d_noa", int64(1), "dwh"}, {day, "deposit_due_7d_nominal", int64(200), "dwh"}, {day, "deposit_due_week_1", int64(200), "dwh"},
			}}, nil
		}
		if strings.Contains(q, "dashboard_maturity_preview") {
			return &testRows{cols: []string{"name", "account", "branch", "due", "amount"}, rows: [][]driver.Value{{"Customer", "D1", "001", day.AddDate(0, 0, 7), int64(200)}}}, nil
		}
		t.Fatalf("source query on cache hit: %s", q)
		return nil, nil
	})
	defer db.Close()
	s := &DashboardService{metrics: NewMetricStore(db)}
	result, err := s.GetDepositMaturities(context.Background(), domain.Filter{Mode: "daily", Date: day, Branch: "001"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Empty || result.Groups[0].Metrics[1].Value != 200 || len(result.Maturities) != 1 || result.Series[0].Points[1].Value != 200 {
		t.Fatalf("maturity=%+v", result)
	}
}

func TestFinancialPageUsesMaterializedValues(t *testing.T) {
	f := domain.Filter{Mode: "daily", Date: testDate("2026-09-24"), Branch: "ALL"}
	db := testDB(func(q string, _ []driver.NamedValue) (driver.Rows, error) {
		if !strings.Contains(q, "dashboard_daily_metrics") {
			t.Fatalf("source query: %s", q)
		}
		r := &testRows{cols: []string{"date", "key", "value", "source"}}
		for _, p := range points(f) {
			d := p.Date
			for name, value := range map[string]int64{"balance_present": 1, "loan_present": 1, "loan_organic_booking": 0, "asset": 1000, "profit_before_tax": 100, "nim": 400, "bopo": 7000, "ldr": 8000, "cash_ratio": 9000, "npl_percentage": 500} {
				r.rows = append(r.rows, metricRowsForDay(d, name, value))
			}
		}
		return r, nil
	})
	defer db.Close()
	s := &DashboardService{metrics: NewMetricStore(db)}
	d, err := s.GetFinancialPerformance(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if d.Empty || d.Groups[0].Metrics[0].Value != 400 || d.Groups[1].Metrics[0].Value != 1000 || d.Series[0].Points[11].Value != 1000 {
		t.Fatalf("financial=%+v", d)
	}
}

func TestMissingRangesDoNotRebuildCompletedDates(t *testing.T) {
	from := testDate("2026-09-21")
	db := testDB(func(q string, _ []driver.NamedValue) (driver.Rows, error) {
		if !strings.Contains(q, "metric_key='savings_present'") {
			t.Fatalf("unexpected query: %s", q)
		}
		return &testRows{cols: []string{"date"}, rows: [][]driver.Value{{from}, {from.AddDate(0, 0, 2)}}}, nil
	})
	defer db.Close()
	ranges, err := NewMetricStore(db).missingRanges(context.Background(), from, from.AddDate(0, 0, 3))
	if err != nil {
		t.Fatal(err)
	}
	if len(ranges) != 2 || !ranges[0][0].Equal(from.AddDate(0, 0, 1)) || !ranges[0][1].Equal(from.AddDate(0, 0, 1)) || !ranges[1][0].Equal(from.AddDate(0, 0, 3)) {
		t.Fatalf("missing ranges=%v", ranges)
	}
}

func TestChannelingFallbackOnlyMissingPoints(t *testing.T) {
	f := domain.Filter{Mode: "daily", Date: testDate("2026-09-24"), Branch: "001"}
	first := points(f)[0].Date
	db := testDB(func(_ string, _ []driver.NamedValue) (driver.Rows, error) {
		r := &testRows{cols: []string{"date", "key", "value"}}
		for _, p := range points(f) {
			if p.Date.Equal(first) {
				continue
			}
			d := p.Date
			r.rows = append(r.rows, []driver.Value{d, "channeling_present", int64(1)}, []driver.Value{d, "channeling_exists", int64(1)}, []driver.Value{d, "loan_channeling_booking", int64(10)}, []driver.Value{d, "loan_channeling_plafond", int64(100)})
		}
		return r, nil
	})
	defer db.Close()
	positions, missing, err := NewMetricStore(db).channeling(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || !missing[0].Date.Equal(first) || len(positions) != 11 {
		t.Fatalf("positions=%d missing=%v", len(positions), missing)
	}
}

func TestMaturityPreviewsAcceptTextDates(t *testing.T) {
	day := testDate("2026-09-24")
	due := testDate("2026-10-01")
	calls := 0
	db := testDB(func(q string, _ []driver.NamedValue) (driver.Rows, error) {
		if !strings.Contains(q, "DATE(maturity_date)") {
			t.Fatalf("maturity date not coerced: %s", q)
		}
		calls++
		branch := "001"
		var date driver.Value = []byte("2026-10-01")
		if calls == 2 {
			branch = "ALL"
			date = due
		}
		return &testRows{cols: []string{"as_of_date", "branch", "slot", "customer_name", "account_no", "account_branch", "maturity_date", "amount"}, rows: [][]driver.Value{{day, branch, int64(1), "Customer", "D1", "001", date, int64(200)}}}, nil
	})
	defer db.Close()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	previews, err := maturityPreviews(context.Background(), reader{tx: tx, tables: historicalTables}, []time.Time{day}, "dwh", 0)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(previews) != 2 || !previews[0].due.Equal(due) || !previews[1].due.Equal(due) {
		t.Fatalf("previews=%+v calls=%d", previews, calls)
	}
}
