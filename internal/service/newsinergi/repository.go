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

func Open(ctx context.Context, dwhDSN string) (*Repository, error) {
	cfg, err := mysql.ParseDSN(dwhDSN)
	if err != nil || cfg.DBName != "dwhv2" {
		return nil, errors.New("invalid DWH_DBSTRING for Newsinergi")
	}
	cfg.DBName = "newsinergi"
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
