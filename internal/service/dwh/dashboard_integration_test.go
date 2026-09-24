package dwh

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/ibldzn/dashboard-roro-jongrang/internal/domain"
)

// Run with DWH_DBSTRING set. All repository queries use read-only transactions.
func TestLiveReconciliation(t *testing.T) {
	dsn := os.Getenv("DWH_DBSTRING")
	if dsn == "" {
		t.Skip("DWH_DBSTRING is not set")
	}
	ctx := context.Background()
	repo, latest, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	s := NewDashboardService(repo)
	f, err := domain.ParseFilterAt("daily", latest.Format("2006-01-02"), "ALL", latest)
	if err != nil {
		t.Fatal(err)
	}
	overview, err := s.GetOverview(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	sdpk, err := s.GetSavings(ctx, f, "dpk")
	if err != nil {
		t.Fatal(err)
	}
	sabp, err := s.GetSavings(ctx, f, "abp")
	if err != nil {
		t.Fatal(err)
	}
	ddpk, err := s.GetDeposits(ctx, f, "dpk")
	if err != nil {
		t.Fatal(err)
	}
	dabp, err := s.GetDeposits(ctx, f, "abp")
	if err != nil {
		t.Fatal(err)
	}
	loans, err := s.GetLoans(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	fin, err := s.GetFinancialPerformance(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	var savings, savingsABP, deposits, depositsABP, outstanding, bad, booking, loanCOA, depositCOA int64
	err = repo.read(ctx, func(q reader) error {
		var readOnly int
		var coaCount int
		if err := q.tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM fincloud_balance_sheet_reports WHERE as_of_date=? AND co_a_no='1'", latest).Scan(&coaCount); err != nil {
			return err
		}
		if coaCount == 0 {
			t.Fatal("no balance sheet rows at latest date")
		}
		if err := q.tx.QueryRowContext(ctx, "SELECT trx_is_read_only FROM information_schema.innodb_trx WHERE trx_mysql_thread_id=CONNECTION_ID()").Scan(&readOnly); err != nil {
			return err
		}
		if readOnly != 1 {
			t.Fatal("DWH transaction is not read-only")
		}
		if err := q.tx.QueryRowContext(ctx, `SELECT
			CAST(ROUND(SUM(CAST(REPLACE(credit_balance,',','') AS DECIMAL(30,2))),0) AS SIGNED),
			CAST(ROUND(SUM(CASE WHEN product_id IN ('119','199') THEN CAST(REPLACE(credit_balance,',','') AS DECIMAL(30,2)) ELSE 0 END),0) AS SIGNED)
			FROM fincloud_eod_savings_balance_details_report
			WHERE as_of_date=? AND product_id NOT IN ('RAK','TAB_INTERNAL')`, latest).Scan(&savings, &savingsABP); err != nil {
			return err
		}
		if err := q.tx.QueryRowContext(ctx, `SELECT
			CAST(ROUND(SUM(CAST(nominal AS DECIMAL(30,2))),0) AS SIGNED),
			CAST(ROUND(SUM(CASE WHEN product_id IN ('203','204') THEN CAST(nominal AS DECIMAL(30,2)) ELSE 0 END),0) AS SIGNED)
			FROM fincloud_eod_time_deposit_account_balance_details WHERE as_of_date=?`, latest).Scan(&deposits, &depositsABP); err != nil {
			return err
		}
		if err := q.tx.QueryRowContext(ctx, `SELECT
			CAST(ROUND(SUM(CAST(sisa_pokok_pinjaman AS DECIMAL(30,2))),0) AS SIGNED),
			CAST(ROUND(SUM(CASE WHEN kolektibilitas_bi IN ('3','4','5') THEN CAST(sisa_pokok_pinjaman AS DECIMAL(30,2)) ELSE 0 END),0) AS SIGNED),
			CAST(ROUND(SUM(CASE WHEN STR_TO_DATE(periode_mulai,'%d/%m/%Y')=as_of_date THEN CAST(pokok_pinjaman AS DECIMAL(30,2)) ELSE 0 END),0) AS SIGNED)
			FROM fincloud_eod_detail_outstanding_rekening_pinjaman WHERE as_of_date=?`, latest).Scan(&outstanding, &bad, &booking); err != nil {
			return err
		}
		return q.tx.QueryRowContext(ctx, `SELECT
			CAST(ROUND(SUM(CASE WHEN co_a_no IN ('121','122') THEN CAST(REPLACE(REPLACE(REPLACE(last_balance,',',''),'<','-'),'>','') AS DECIMAL(30,2)) ELSE 0 END),0) AS SIGNED),
			CAST(ROUND(SUM(CASE WHEN co_a_no IN ('221','2312200','2312201') THEN -CAST(REPLACE(REPLACE(REPLACE(last_balance,',',''),'<','-'),'>','') AS DECIMAL(30,2)) WHEN co_a_no IN ('2212111','2212116','2212199') THEN CAST(REPLACE(REPLACE(REPLACE(last_balance,',',''),'<','-'),'>','') AS DECIMAL(30,2)) ELSE 0 END),0) AS SIGNED)
			FROM fincloud_balance_sheet_reports WHERE as_of_date=?`, latest).Scan(&loanCOA, &depositCOA)
	})
	if err != nil {
		t.Fatal(err)
	}
	check := func(name string, got, want int64) {
		if got != want {
			t.Errorf("%s: got %d, want %d", name, got, want)
		}
	}
	check("savings DPK+ABP", sdpk.Metrics[0].Value+sabp.Metrics[0].Value, savings)
	check("savings ABP", sabp.Metrics[0].Value, savingsABP)
	check("overview savings", overview.Metrics[0].Value, savings)
	check("deposits DPK+ABP", ddpk.Metrics[0].Value+dabp.Metrics[0].Value, deposits)
	check("deposits ABP", dabp.Metrics[0].Value, depositsABP)
	check("overview deposits", overview.Metrics[1].Value, deposits)
	check("organik BADE", loans.Groups[1].Metrics[1].Value, outstanding)
	check("overview BADE", overview.Metrics[2].Value, outstanding)
	check("organik booking", loans.Groups[1].Metrics[0].Value, booking)
	check("channeling booking", loans.Groups[0].Metrics[0].Value, 0)
	check("channeling BADE", loans.Groups[0].Metrics[1].Value, 0)
	check("NPL", overview.Secondary[1].Value, percent(bad, outstanding))
	check("LDR", overview.Secondary[0].Value, percent(loanCOA, depositCOA))
	check("financial NPL", fin.Groups[0].Metrics[4].Value, overview.Secondary[1].Value)
	check("financial LDR", fin.Groups[0].Metrics[1].Value, overview.Secondary[0].Value)
	mat, err := s.GetDepositMaturities(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range mat.Groups {
		nf := domain.NominativeFilter{Filter: f, Domain: "deposito", Category: "jatuh-tempo", Metric: "balance", Bucket: b.Metrics[0].Bucket}
		rows, err := s.GetNominative(ctx, nf)
		if err != nil {
			t.Fatal(err)
		}
		check("maturity "+b.Title+" NOA", b.Metrics[0].Value, int64(rows.Count))
		check("maturity "+b.Title+" nominal", b.Metrics[1].Value, rows.Total)
	}
	for _, tc := range []struct {
		domain, category, metric string
		want                     int64
	}{
		{"tabungan", "dpk", "balance", sdpk.Metrics[0].Value},
		{"tabungan", "abp", "balance", sabp.Metrics[0].Value},
		{"deposito", "dpk", "balance", ddpk.Metrics[0].Value},
		{"deposito", "abp", "balance", dabp.Metrics[0].Value},
		{"kredit", "organik", "bade", outstanding},
	} {
		rows, err := s.GetNominative(ctx, domain.NominativeFilter{Filter: f, Domain: tc.domain, Category: tc.category, Metric: tc.metric})
		if err != nil {
			t.Fatal(err)
		}
		check("nominative "+tc.domain+" "+tc.category, rows.Total, tc.want)
	}
	mf, err := domain.ParseFilterAt("monthly", latest.Format("2006-01"), "ALL", latest)
	if err != nil {
		t.Fatal(err)
	}
	ml, err := s.GetLoans(ctx, mf)
	if err != nil {
		t.Fatal(err)
	}
	var monthlyBooking int64
	err = repo.read(ctx, func(q reader) error {
		return q.tx.QueryRowContext(ctx, `SELECT CAST(ROUND(SUM(CASE WHEN STR_TO_DATE(periode_mulai,'%d/%m/%Y') BETWEEN ? AND ? THEN CAST(pokok_pinjaman AS DECIMAL(30,2)) ELSE 0 END),0) AS SIGNED) FROM fincloud_eod_detail_outstanding_rekening_pinjaman WHERE as_of_date=?`, domain.PeriodStart(mf), mf.Date, mf.Date).Scan(&monthlyBooking)
	})
	if err != nil {
		t.Fatal(err)
	}
	check("monthly booking", ml.Groups[1].Metrics[0].Value, monthlyBooking)
	booked, err := s.GetNominative(ctx, domain.NominativeFilter{Filter: mf, Domain: "kredit", Category: "organik", Metric: "booking"})
	if err != nil {
		t.Fatal(err)
	}
	check("monthly booking nominative", booked.Total, monthlyBooking)
}

func TestLiveFinancialRatios(t *testing.T) {
	dsn := os.Getenv("DWH_DBSTRING")
	if dsn == "" {
		t.Skip("DWH_DBSTRING is not set")
	}
	ctx := context.Background()
	repo, latest, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	s := NewDashboardService(repo)
	for _, branch := range []string{"ALL", "001"} {
		f, err := domain.ParseFilterAt("daily", latest.Format("2006-01-02"), branch, latest)
		if err != nil {
			t.Fatal(err)
		}
		d, err := s.GetFinancialPerformance(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		actual := map[string]int64{}
		for _, g := range d.Groups {
			for _, metric := range g.Metrics {
				actual[metric.Label] = metric.Value
			}
		}
		components := map[string]int64{}
		var productive int64
		err = repo.read(ctx, func(q reader) error {
			where := "as_of_date=?"
			args := []any{latest}
			if branch != "ALL" {
				where += " AND source_location_id=?"
				args = append(args, branch)
			}
			rows, err := q.tx.QueryContext(ctx, `SELECT co_a_no,
				CAST(ROUND(SUM(CAST(REPLACE(REPLACE(REPLACE(last_balance,',',''),'<','-'),'>','') AS DECIMAL(30,2)) * CASE WHEN LEFT(co_a_no,1) IN ('2','3','4','6') THEN -1 ELSE 1 END),0) AS SIGNED)
				FROM fincloud_balance_sheet_reports WHERE `+where+` GROUP BY co_a_no`, args...)
			if err != nil {
				return err
			}
			for rows.Next() {
				var coa string
				var amount int64
				if err := rows.Scan(&coa, &amount); err != nil {
					rows.Close()
					return err
				}
				components[coa] = amount
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()
			months := make([]any, 0, int(latest.Month()))
			marks := make([]string, 0, int(latest.Month()))
			for month := 1; month <= int(latest.Month()); month++ {
				months = append(months, nimDate(latest, month))
				marks = append(marks, "?")
			}
			query := `SELECT CAST(ROUND(SUM(CAST(REPLACE(REPLACE(REPLACE(last_balance,',',''),'<','-'),'>','') AS DECIMAL(30,2))),0) AS SIGNED)
				FROM fincloud_balance_sheet_reports WHERE as_of_date IN (` + strings.Join(marks, ",") + `) AND co_a_no IN ('110','121')`
			queryArgs := months
			if branch != "ALL" {
				query += " AND source_location_id=?"
				queryArgs = append(queryArgs, branch)
			}
			return q.tx.QueryRowContext(ctx, query, queryArgs...).Scan(&productive)
		})
		if err != nil {
			t.Fatal(err)
		}
		get := func(keys ...string) int64 {
			var v int64
			for _, k := range keys {
				v += components[k]
			}
			return v
		}
		want := map[string]int64{
			"Total Aset":         get("1"),
			"Laba Sebelum Pajak": get("323", "558"),
			"BOPO":               percent(get("5")-get("558"), get("4")),
			"NIM":                percent((get("401", "402", "403", "410")-get("501", "502", "511"))*12, productive),
			"LDR":                percent(get("121", "122"), get("221", "2312200", "2312201")-get("2212111", "2212116", "2212199")),
			"Cash Ratio":         percent(get("100", "111", "112"), get("211", "212", "213", "219", "2011008", "2011001", "2011004", "2011005", "2011006", "2011007", "208", "221", "2312200", "2312201")),
		}
		for name, expected := range want {
			if actual[name] != expected {
				t.Errorf("%s %s: got %d, want %d", branch, name, actual[name], expected)
			}
		}
	}
}
