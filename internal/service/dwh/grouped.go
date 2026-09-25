package dwh

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Grouped source queries serve the background materializer. Their projections
// are shared with request fallback queries in repository.go.
func (q reader) groupedFunding(ctx context.Context, days []time.Time, table, balance, branchCol, abp string, excluded bool) (map[string]map[string]fundingPosition, error) {
	mark, args := dates(days)
	where := fundingExclusion(excluded)
	query := fmt.Sprintf(`SELECT as_of_date,LEFT(%s,3),%s FROM %s WHERE as_of_date IN (%s)%s
 GROUP BY as_of_date,LEFT(%s,3) WITH ROLLUP`, branchCol, fundingProjection(balance, abp), table, mark, where, branchCol)
	rows, err := q.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]fundingPosition{}
	for rows.Next() {
		var d sql.NullTime
		var b sql.NullString
		var p fundingPosition
		if err = rows.Scan(&d, &b, &p.Total, &p.ABP, &p.NOA, &p.ABPNOA); err != nil {
			return nil, err
		}
		if !d.Valid {
			continue
		}
		branch := "ALL"
		if b.Valid {
			branch = b.String
		}
		if out[branch] == nil {
			out[branch] = map[string]fundingPosition{}
		}
		out[branch][key(d.Time)] = p
	}
	return out, rows.Err()
}

func (q reader) groupedLoans(ctx context.Context, days []time.Time) (map[string]map[string]loanPosition, error) {
	mark, args := dates(days)
	query := fmt.Sprintf(`SELECT as_of_date,LEFT(cabang_rekening,3),%s FROM %s WHERE as_of_date IN (%s)
 GROUP BY as_of_date,LEFT(cabang_rekening,3) WITH ROLLUP`, loanProjection("daily"), q.tables.loans, mark)
	rows, err := q.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]loanPosition{}
	for rows.Next() {
		var d sql.NullTime
		var b sql.NullString
		var p loanPosition
		if err = rows.Scan(&d, &b, &p.Outstanding, &p.Bad, &p.Booking); err != nil {
			return nil, err
		}
		if !d.Valid {
			continue
		}
		branch := "ALL"
		if b.Valid {
			branch = b.String
		}
		if out[branch] == nil {
			out[branch] = map[string]loanPosition{}
		}
		out[branch][key(d.Time)] = p
	}
	return out, rows.Err()
}

func (q reader) groupedBalance(ctx context.Context, days []time.Time) (map[string]map[string]balancePosition, error) {
	mark, args := dates(days)
	query := fmt.Sprintf(`SELECT as_of_date,co_a_no,source_location_id,%s FROM %s
 WHERE as_of_date IN (%s) AND co_a_no IN (%s)
 GROUP BY as_of_date,co_a_no,source_location_id WITH ROLLUP`, balanceProjection(), q.tables.balance, mark, financialCOAs)
	rows, err := q.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]balancePosition{}
	for rows.Next() {
		var d sql.NullTime
		var coa, branch sql.NullString
		var value int64
		if err = rows.Scan(&d, &coa, &branch, &value); err != nil {
			return nil, err
		}
		if !d.Valid || !coa.Valid {
			continue
		}
		b := "ALL"
		if branch.Valid {
			b = branch.String
		}
		if out[b] == nil {
			out[b] = map[string]balancePosition{}
		}
		k := key(d.Time)
		if out[b][k] == nil {
			out[b][k] = balancePosition{}
		}
		out[b][k][coa.String] = value
	}
	return out, rows.Err()
}
