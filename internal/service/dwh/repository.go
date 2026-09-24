package dwh

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

const (
	savingsTable = "fincloud_eod_savings_balance_details_report"
	depositTable = "fincloud_eod_time_deposit_account_balance_details"
	loanTable    = "fincloud_eod_detail_outstanding_rekening_pinjaman"
	balanceTable = "fincloud_balance_sheet_reports"
)

type Repository struct{ db *sql.DB }
type reader struct{ tx *sql.Tx }

type fundingPosition struct{ Total, ABP, NOA, ABPNOA int64 }
type loanPosition struct{ Outstanding, Bad, Booking int64 }
type balancePosition map[string]int64

type maturityRow struct {
	Name, Account, Branch string
	Due                   time.Time
	AmountCents           int64
}

func Open(ctx context.Context, dsn string) (*Repository, time.Time, error) {
	if dsn == "" {
		return nil, time.Time{}, errors.New("DWH_DBSTRING is required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, time.Time{}, errors.New("invalid DWH_DBSTRING")
	}
	cfg.ParseTime = true
	cfg.MultiStatements = false
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 30 * time.Second
	if cfg.DBName != "dwhv2" {
		return nil, time.Time{}, errors.New("DWH_DBSTRING must target dwhv2")
	}
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, time.Time{}, errors.New("opening DWH failed")
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)
	r := &Repository{db: db}
	var latest time.Time
	err = r.read(ctx, func(q reader) error {
		return q.tx.QueryRowContext(ctx, `SELECT LEAST(
			(SELECT MAX(as_of_date) FROM fincloud_eod_savings_balance_details_report),
			(SELECT MAX(as_of_date) FROM fincloud_eod_time_deposit_account_balance_details),
			(SELECT MAX(as_of_date) FROM fincloud_eod_detail_outstanding_rekening_pinjaman),
			(SELECT MAX(as_of_date) FROM fincloud_balance_sheet_reports))`).Scan(&latest)
	})
	if err != nil {
		db.Close()
		return nil, time.Time{}, fmt.Errorf("reading DWH reporting date: %w", err)
	}
	return r, latest, nil
}

func (r *Repository) Close() error { return r.db.Close() }

func (r *Repository) read(ctx context.Context, fn func(reader) error) error {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(reader{tx}); err != nil {
		return err
	}
	return tx.Commit()
}

func money(col string) string {
	return "CAST(NULLIF(REPLACE(REPLACE(REPLACE(" + col + ", ',', ''), '<', '-'), '>', ''), '') AS DECIMAL(30,2))"
}

func dates(d []time.Time) (string, []any) {
	marks := make([]string, len(d))
	args := make([]any, len(d))
	for i, day := range d {
		marks[i] = "?"
		args[i] = day
	}
	return strings.Join(marks, ","), args
}

func scope(col, branch string) (string, []any) {
	if branch == "ALL" {
		return "", nil
	}
	if col == "source_location_id" {
		return " AND source_location_id = ?", []any{branch}
	}
	return " AND LEFT(" + col + ", 3) = ?", []any{branch}
}

func (q reader) funding(ctx context.Context, table, balance, branchCol, abp string, excluded bool, days []time.Time, branch string) (map[string]fundingPosition, error) {
	mark, args := dates(days)
	where, bargs := scope(branchCol, branch)
	args = append(args, bargs...)
	if excluded {
		where += " AND product_id NOT IN ('RAK','TAB_INTERNAL')"
	}
	amount := money(balance)
	query := fmt.Sprintf(`SELECT as_of_date,
		CAST(ROUND(COALESCE(SUM(%s),0),0) AS SIGNED),
		CAST(ROUND(COALESCE(SUM(CASE WHEN product_id IN (%s) THEN %s ELSE 0 END),0),0) AS SIGNED),
		COUNT(*), SUM(CASE WHEN product_id IN (%s) THEN 1 ELSE 0 END)
		FROM %s WHERE as_of_date IN (%s)%s GROUP BY as_of_date`, amount, abp, amount, abp, table, mark, where)
	rows, err := q.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]fundingPosition{}
	for rows.Next() {
		var day time.Time
		var p fundingPosition
		if err := rows.Scan(&day, &p.Total, &p.ABP, &p.NOA, &p.ABPNOA); err != nil {
			return nil, err
		}
		out[day.Format("2006-01-02")] = p
	}
	return out, rows.Err()
}

func (q reader) SavingsPosition(ctx context.Context, days []time.Time, branch string) (map[string]fundingPosition, error) {
	return q.funding(ctx, savingsTable, "credit_balance", "branch", "'119','199'", true, days, branch)
}

func (q reader) DepositPosition(ctx context.Context, days []time.Time, branch string) (map[string]fundingPosition, error) {
	return q.funding(ctx, depositTable, "nominal", "branch_code", "'203','204'", false, days, branch)
}

func periodStartSQL(mode string) string {
	switch mode {
	case "daily":
		return "as_of_date"
	case "yearly":
		return "MAKEDATE(YEAR(as_of_date), 1)"
	default:
		return "DATE_FORMAT(as_of_date, '%Y-%m-01')"
	}
}

func (q reader) LoanPosition(ctx context.Context, days []time.Time, branch, mode string) (map[string]loanPosition, error) {
	mark, args := dates(days)
	where, bargs := scope("cabang_rekening", branch)
	args = append(args, bargs...)
	outstanding, original := money("sisa_pokok_pinjaman"), money("pokok_pinjaman")
	query := fmt.Sprintf(`SELECT as_of_date,
		CAST(ROUND(COALESCE(SUM(%s),0),0) AS SIGNED),
		CAST(ROUND(COALESCE(SUM(CASE WHEN kolektibilitas_bi IN ('3','4','5') THEN %s ELSE 0 END),0),0) AS SIGNED),
		CAST(ROUND(COALESCE(SUM(CASE WHEN STR_TO_DATE(periode_mulai,'%%d/%%m/%%Y') BETWEEN %s AND as_of_date THEN %s ELSE 0 END),0),0) AS SIGNED)
		FROM %s WHERE as_of_date IN (%s)%s GROUP BY as_of_date`, outstanding, outstanding, periodStartSQL(mode), original, loanTable, mark, where)
	rows, err := q.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]loanPosition{}
	for rows.Next() {
		var day time.Time
		var p loanPosition
		if err := rows.Scan(&day, &p.Outstanding, &p.Bad, &p.Booking); err != nil {
			return nil, err
		}
		out[day.Format("2006-01-02")] = p
	}
	return out, rows.Err()
}

const financialCOAs = "'1','323','558','5','4','401','402','403','410','501','502','511','110','121','122','100','111','112','211','212','213','219','2011008','2011001','2011004','2011005','2011006','2011007','208','221','2312200','2312201','2212111','2212116','2212199'"

func (q reader) BalanceSheetPosition(ctx context.Context, days []time.Time, branch string) (map[string]balancePosition, error) {
	mark, args := dates(days)
	where, bargs := scope("source_location_id", branch)
	args = append(args, bargs...)
	query := fmt.Sprintf(`SELECT as_of_date, co_a_no,
		CAST(ROUND(COALESCE(SUM(%s * CASE WHEN LEFT(co_a_no,1) IN ('2','3','4','6') THEN -1 ELSE 1 END),0),0) AS SIGNED)
		FROM %s WHERE as_of_date IN (%s) AND co_a_no IN (%s)%s
		GROUP BY as_of_date, co_a_no`, money("last_balance"), balanceTable, mark, financialCOAs, where)
	rows, err := q.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]balancePosition{}
	for rows.Next() {
		var day time.Time
		var coa string
		var value int64
		if err := rows.Scan(&day, &coa, &value); err != nil {
			return nil, err
		}
		key := day.Format("2006-01-02")
		if out[key] == nil {
			out[key] = balancePosition{}
		}
		out[key][coa] = value
	}
	return out, rows.Err()
}

func (q reader) Maturities(ctx context.Context, day time.Time, branch string) ([]maturityRow, error) {
	where, bargs := scope("branch_code", branch)
	args := append([]any{day}, bargs...)
	query := fmt.Sprintf(`SELECT customer_name,account_no,LEFT(branch_code,3),DATE(maturity_date),
		CAST(ROUND(COALESCE(%s,0)*100,0) AS SIGNED)
		FROM %s WHERE as_of_date=?%s AND maturity_date >= ?
		ORDER BY maturity_date,account_no`, money("nominal"), depositTable, where)
	args = append(args, day)
	rows, err := q.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []maturityRow
	for rows.Next() {
		var x maturityRow
		if err := rows.Scan(&x.Name, &x.Account, &x.Branch, &x.Due, &x.AmountCents); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
