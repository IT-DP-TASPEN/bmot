package realtime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

type Saving struct{ Branch, Product, Account, Customer, CIF, Balance string }
type Deposit struct {
	Branch, Product, Account, Customer, CIF, Nominal string
	Maturity                                         time.Time
}
type Loan struct {
	Branch, Product, Account, Customer, CIF, Principal, Outstanding, Collectibility string
	Start                                                                           time.Time
}
type Balance struct{ Branch, COA, Name, Amount string }

type Dataset struct {
	Date     time.Time
	Savings  []Saving
	Deposits []Deposit
	Loans    []Loan
	Balance  []Balance
}

type Published struct {
	ID                int64
	Date, PublishedAt time.Time
}

type Store struct{ db *sql.DB }

func Open(ctx context.Context, dsn string) (*Store, error) {
	if dsn == "" {
		return nil, errors.New("APP_DBSTRING is required for hybrid mode")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, errors.New("invalid APP_DBSTRING")
	}
	if cfg.DBName == "" || strings.EqualFold(cfg.DBName, "dwhv2") {
		return nil, errors.New("APP_DBSTRING must name a separate local application database")
	}
	cfg.ParseTime = true
	cfg.MultiStatements = false
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 30 * time.Second
	cfg.WriteTimeout = 30 * time.Second
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, errors.New("opening application database failed")
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, errors.New("application database unavailable")
	}
	s := &Store{db: db}
	if err := s.init(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("initializing local snapshots: %w", err)
	}
	return s, nil
}

func (s *Store) DB() *sql.DB  { return s.db }
func (s *Store) Close() error { return s.db.Close() }

// All schema changes below target APP_DBSTRING. The DWH connection is never passed here.
func (s *Store) init(ctx context.Context) error {
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS dashboard_snapshot_runs (
		 id BIGINT AUTO_INCREMENT PRIMARY KEY, status VARCHAR(16) NOT NULL,
		 source_business_date DATE NOT NULL, started_at DATETIME(6) NOT NULL,
		 completed_at DATETIME(6) NULL, published_at DATETIME(6) NULL,
		 failure_message VARCHAR(500) NULL, created_at DATETIME(6) NOT NULL,
		 INDEX (status,id))`,
		`CREATE TABLE IF NOT EXISTS dashboard_rt_savings (
		 snapshot_id BIGINT NOT NULL, as_of_date DATE NOT NULL, branch VARCHAR(100) NOT NULL,
		 product_id VARCHAR(255) NOT NULL, account_no VARCHAR(80) NOT NULL,
		 customer_name VARCHAR(255) NOT NULL, cif_no VARCHAR(80) NOT NULL,
		 credit_balance DECIMAL(30,2) NOT NULL,
		 PRIMARY KEY (snapshot_id,account_no), INDEX (snapshot_id,branch),
		 FOREIGN KEY (snapshot_id) REFERENCES dashboard_snapshot_runs(id) ON DELETE CASCADE)`,
		`CREATE TABLE IF NOT EXISTS dashboard_rt_time_deposits (
		 snapshot_id BIGINT NOT NULL, as_of_date DATE NOT NULL, branch_code VARCHAR(100) NOT NULL,
		 product_id VARCHAR(255) NOT NULL, account_no VARCHAR(80) NOT NULL,
		 customer_name VARCHAR(255) NOT NULL, cif_no VARCHAR(80) NOT NULL,
		 nominal DECIMAL(30,2) NOT NULL, maturity_date DATE NOT NULL,
		 PRIMARY KEY (snapshot_id,account_no), INDEX (snapshot_id,branch_code),
		 FOREIGN KEY (snapshot_id) REFERENCES dashboard_snapshot_runs(id) ON DELETE CASCADE)`,
		`CREATE TABLE IF NOT EXISTS dashboard_rt_loans (
		 snapshot_id BIGINT NOT NULL, as_of_date DATE NOT NULL, cabang_rekening VARCHAR(100) NOT NULL,
		 produk VARCHAR(255) NOT NULL, no_rekening VARCHAR(80) NOT NULL,
		 nama_nasabah VARCHAR(255) NOT NULL, no_cif VARCHAR(80) NOT NULL,
		 periode_mulai VARCHAR(10) NOT NULL, pokok_pinjaman DECIMAL(30,2) NOT NULL,
		 sisa_pokok_pinjaman DECIMAL(30,2) NOT NULL, kolektibilitas_bi VARCHAR(2) NOT NULL,
		 PRIMARY KEY (snapshot_id,no_rekening), INDEX (snapshot_id,cabang_rekening),
		 FOREIGN KEY (snapshot_id) REFERENCES dashboard_snapshot_runs(id) ON DELETE CASCADE)`,
		`CREATE TABLE IF NOT EXISTS dashboard_rt_balance_sheet (
		 snapshot_id BIGINT NOT NULL, as_of_date DATE NOT NULL, source_location_id VARCHAR(3) NOT NULL,
		 co_a_no VARCHAR(32) NOT NULL, chart_of_account VARCHAR(255) NOT NULL,
		 last_balance DECIMAL(30,2) NOT NULL,
		 PRIMARY KEY (snapshot_id,source_location_id,co_a_no),
		 FOREIGN KEY (snapshot_id) REFERENCES dashboard_snapshot_runs(id) ON DELETE CASCADE)`,
	} {
		if _, err := s.db.ExecContext(ctx, ddl); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Latest(ctx context.Context) (*Published, error) {
	var p Published
	err := s.db.QueryRowContext(ctx, `SELECT id,source_business_date,published_at FROM dashboard_snapshot_runs WHERE status='published' ORDER BY id DESC LIMIT 1`).Scan(&p.ID, &p.Date, &p.PublishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) Start(ctx context.Context, date time.Time) (int64, error) {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `INSERT INTO dashboard_snapshot_runs (status,source_business_date,started_at,created_at) VALUES ('loading',?,?,?)`, date, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) Fail(ctx context.Context, id int64, diagnostic string) error {
	if len(diagnostic) > 500 {
		diagnostic = diagnostic[:500]
	}
	_, err := s.db.ExecContext(ctx, `UPDATE dashboard_snapshot_runs SET status='failed',completed_at=?,failure_message=? WHERE id=? AND status='loading'`, time.Now().UTC(), diagnostic, id)
	return err
}

func (s *Store) Publish(ctx context.Context, id int64, d Dataset) error {
	if err := validate(d); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE dashboard_snapshot_runs SET status='validating' WHERE id=? AND status='loading' AND source_business_date=?`, id, d.Date)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("snapshot generation is not loading")
	}
	day := d.Date.Format("2006-01-02")
	if err := insertBatches(ctx, tx, "dashboard_rt_savings", []string{"snapshot_id", "as_of_date", "branch", "product_id", "account_no", "customer_name", "cif_no", "credit_balance"}, len(d.Savings), func(i int) []any {
		x := d.Savings[i]
		return []any{id, day, x.Branch, x.Product, x.Account, x.Customer, x.CIF, x.Balance}
	}); err != nil {
		return err
	}
	if err := insertBatches(ctx, tx, "dashboard_rt_time_deposits", []string{"snapshot_id", "as_of_date", "branch_code", "product_id", "account_no", "customer_name", "cif_no", "nominal", "maturity_date"}, len(d.Deposits), func(i int) []any {
		x := d.Deposits[i]
		return []any{id, day, x.Branch, x.Product, x.Account, x.Customer, x.CIF, x.Nominal, x.Maturity.Format("2006-01-02")}
	}); err != nil {
		return err
	}
	if err := insertBatches(ctx, tx, "dashboard_rt_loans", []string{"snapshot_id", "as_of_date", "cabang_rekening", "produk", "no_rekening", "nama_nasabah", "no_cif", "periode_mulai", "pokok_pinjaman", "sisa_pokok_pinjaman", "kolektibilitas_bi"}, len(d.Loans), func(i int) []any {
		x := d.Loans[i]
		return []any{id, day, x.Branch, x.Product, x.Account, x.Customer, x.CIF, x.Start.Format("02/01/2006"), x.Principal, x.Outstanding, x.Collectibility}
	}); err != nil {
		return err
	}
	if err := insertBatches(ctx, tx, "dashboard_rt_balance_sheet", []string{"snapshot_id", "as_of_date", "source_location_id", "co_a_no", "chart_of_account", "last_balance"}, len(d.Balance), func(i int) []any { x := d.Balance[i]; return []any{id, day, x.Branch, x.COA, x.Name, x.Amount} }); err != nil {
		return err
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE dashboard_snapshot_runs SET status='published',completed_at=?,published_at=? WHERE id=? AND status='validating'`, now, now, id); err != nil {
		return err
	}
	return tx.Commit()
}

func insertBatches(ctx context.Context, tx *sql.Tx, table string, columns []string, count int, row func(int) []any) error {
	const batch = 250
	for start := 0; start < count; start += batch {
		end := min(start+batch, count)
		var b strings.Builder
		b.WriteString("INSERT INTO " + table + " (" + strings.Join(columns, ",") + ") VALUES ")
		args := make([]any, 0, (end-start)*len(columns))
		for i := start; i < end; i++ {
			if i > start {
				b.WriteByte(',')
			}
			b.WriteByte('(')
			for j := range columns {
				if j > 0 {
					b.WriteByte(',')
				}
				b.WriteByte('?')
			}
			b.WriteByte(')')
			args = append(args, row(i)...)
		}
		if _, err := tx.ExecContext(ctx, b.String(), args...); err != nil {
			return err
		}
	}
	return nil
}

func validate(d Dataset) error {
	if d.Date.IsZero() || len(d.Savings) == 0 || len(d.Deposits) == 0 || len(d.Loans) == 0 || len(d.Balance) == 0 {
		return errors.New("snapshot requires all four nonempty datasets and a business date")
	}
	checkBranch := func(s string) bool {
		return len(s) == 3 && s[:2] == "00" && s[2] >= '0' && s[2] <= '8'
	}
	seen := map[string]bool{}
	for _, x := range d.Savings {
		if x.Account == "" || x.Product == "" || !checkBranch(x.Branch) || seen[x.Account] {
			return errors.New("invalid or duplicate Savings row")
		}
		seen[x.Account] = true
	}
	clear(seen)
	for _, x := range d.Deposits {
		if x.Account == "" || x.Product == "" || !checkBranch(x.Branch) || x.Maturity.IsZero() || seen[x.Account] {
			return errors.New("invalid or duplicate Deposit row")
		}
		seen[x.Account] = true
	}
	clear(seen)
	for _, x := range d.Loans {
		if x.Account == "" || !checkBranch(x.Branch) || x.Start.IsZero() || x.Start.After(d.Date) || seen[x.Account] {
			return errors.New("invalid or duplicate Loan row")
		}
		seen[x.Account] = true
	}
	clear(seen)
	for _, x := range d.Balance {
		if x.COA == "" || !checkBranch(x.Branch) || seen[x.Branch+"/"+x.COA] {
			return errors.New("invalid or duplicate Balance Sheet row")
		}
		seen[x.Branch+"/"+x.COA] = true
	}
	return nil
}

func (s *Store) Retain(ctx context.Context, n int) error {
	if n < 1 {
		n = 1
	}
	for _, status := range []string{"published", "failed"} {
		rows, err := s.db.QueryContext(ctx, `SELECT id FROM dashboard_snapshot_runs WHERE status=? ORDER BY id DESC`, status)
		if err != nil {
			return err
		}
		var old []int64
		for i := 0; rows.Next(); i++ {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			if i >= n {
				old = append(old, id)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, id := range old {
			if _, err := s.db.ExecContext(ctx, `DELETE FROM dashboard_snapshot_runs WHERE id=? AND status=?`, id, status); err != nil {
				return err
			}
		}
	}
	// A process crash can leave a loading generation behind; active refreshes time out in 10 minutes.
	_, err := s.db.ExecContext(ctx, `DELETE FROM dashboard_snapshot_runs WHERE status='loading' AND started_at < UTC_TIMESTAMP() - INTERVAL 1 DAY`)
	return err
}
