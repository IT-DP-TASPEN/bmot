package newsinergi

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/ibldzn/dashboard-roro-jongrang/internal/domain"
)

type Repository struct{ db *sql.DB }

type Position struct {
	Booking, Plafond int64
	Exists           bool
}

type DailyBooking struct {
	Date    time.Time
	Branch  string
	Booking int64
	Count   int64
}

// Daily aggregates signed applications once for a materialization range.
func (r *Repository) Daily(ctx context.Context, through time.Time) ([]DailyBooking, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT DATE(ca.tanggal_realisasi),rb.code,COUNT(*),
		CAST(ROUND(COALESCE(SUM(ca.plafond_kredit),0),0) AS SIGNED)
		FROM credit_applications ca JOIN branches rb ON rb.id=ca.requestor_branch_id
		WHERE ca.status='pk_signed' AND rb.company_id=1 AND ca.tanggal_realisasi < ?
		GROUP BY DATE(ca.tanggal_realisasi),rb.code`, through.AddDate(0, 0, 1))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DailyBooking
	for rows.Next() {
		var x DailyBooking
		if err = rows.Scan(&x.Date, &x.Branch, &x.Count, &x.Booking); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

func Open(ctx context.Context, dwhDSN string) (*Repository, error) {
	cfg, err := mysql.ParseDSN(dwhDSN)
	if err != nil || cfg.DBName != "dwhv2" {
		return nil, errors.New("invalid DWH_DBSTRING for Newsinergi")
	}
	cfg.DBName = "newsinergi"
	cfg.ParseTime = true
	cfg.MultiStatements = false
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, errors.New("opening Newsinergi failed")
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, errors.New("read-only Newsinergi connection unavailable")
	}
	db.SetMaxOpenConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)
	return &Repository{db: db}, nil
}

func (r *Repository) Close() error { return r.db.Close() }

// Positions includes only signed Bank DP Taspen applications through each reporting date.
// Branch audit: company_id 1 is Bank DP Taspen; requestor_branch_id maps codes 001-008.
func (r *Repository) Positions(ctx context.Context, filters []domain.Filter) (map[string]Position, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	const query = `SELECT COUNT(*),
		CAST(ROUND(COALESCE(SUM(CASE WHEN ca.tanggal_realisasi BETWEEN ? AND ? THEN ca.plafond_kredit ELSE 0 END),0),0) AS SIGNED),
		CAST(ROUND(COALESCE(SUM(ca.plafond_kredit),0),0) AS SIGNED)
		FROM credit_applications ca
		JOIN branches rb ON rb.id = ca.requestor_branch_id
		WHERE ca.status = 'pk_signed' AND rb.company_id = 1
		AND ca.tanggal_realisasi <= ? AND (? = 'ALL' OR rb.code = ?)`
	out := make(map[string]Position, len(filters))
	for _, f := range filters {
		var count int
		var p Position
		err := tx.QueryRowContext(ctx, query, domain.PeriodStart(f), f.Date, f.Date, f.Branch, f.Branch).Scan(&count, &p.Booking, &p.Plafond)
		if err != nil {
			return nil, err
		}
		p.Exists = count > 0
		out[f.Date.Format("2006-01-02")] = p
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}
