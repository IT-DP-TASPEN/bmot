package dwh

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/ibldzn/dashboard-roro-jongrang/internal/domain"
)

func (s *DashboardService) GetNominative(ctx context.Context, f domain.NominativeFilter) (domain.NominativeResult, error) {
	allowed := map[string]map[string]bool{"tabungan": {"balance": true, "noa": true}, "deposito": {"balance": true, "noa": true}, "kredit": {"booking": true, "bade": true}}
	if !allowed[f.Domain][f.Metric] {
		return domain.NominativeResult{}, errors.New("metrik nominatif tidak valid")
	}
	if f.Domain == "kredit" && f.Category != "organik" && f.Category != "channeling" && f.Category != "all" {
		return domain.NominativeResult{}, errors.New("kategori kredit tidak valid")
	}
	if f.Domain != "kredit" && f.Category != "dpk" && f.Category != "abp" && f.Category != "all" && !(f.Domain == "deposito" && f.Category == "jatuh-tempo") {
		return domain.NominativeResult{}, errors.New("kategori tidak valid")
	}
	if f.Bucket != "" && (f.Category != "jatuh-tempo" || f.Bucket != "0-7" && f.Bucket != "8-30" && f.Bucket != "31+") {
		return domain.NominativeResult{}, errors.New("rentang jatuh tempo tidak valid")
	}
	res := domain.NominativeResult{Title: fmt.Sprintf("Nominatif %s %s", strings.Title(f.Domain), strings.ToUpper(f.Category)), MetricLabel: map[string]string{"balance": "Saldo", "noa": "NOA", "booking": "Booking", "bade": "BADE"}[f.Metric]}
	if f.Domain == "deposito" && f.Metric == "balance" {
		res.MetricLabel = "Nominal"
	}
	if f.Category == "channeling" {
		res.Page = 1
		res.Pages = 1
		return res, nil
	}
	err := s.sourceFor(f.Date).read(ctx, func(q reader) (err error) { res, err = q.Nominative(ctx, f, res); return err })
	return res, err
}

func (q reader) Nominative(ctx context.Context, f domain.NominativeFilter, res domain.NominativeResult) (domain.NominativeResult, error) {
	var table, branchCol, nameCol, accountCol, cifCol, productCol, collectCol, amount, due string
	switch f.Domain {
	case "tabungan":
		table, branchCol, nameCol, accountCol, cifCol, productCol, collectCol, amount, due = q.tables.savings, "branch", "customer_name", "account_no", "cif_no", "product_id", "''", money("credit_balance"), "NULL"
	case "deposito":
		table, branchCol, nameCol, accountCol, cifCol, productCol, collectCol, amount, due = q.tables.deposits, "branch_code", "customer_name", "account_no", "cif_no", "product_id", "''", money("nominal"), "DATE(maturity_date)"
	default:
		table, branchCol, nameCol, accountCol, cifCol, productCol, collectCol, amount, due = q.tables.loans, "cabang_rekening", "nama_nasabah", "no_rekening", "no_cif", "produk", "kolektibilitas_bi", money("sisa_pokok_pinjaman"), "NULL"
	}
	where := "as_of_date = ?"
	args := []any{f.Date}
	branch, bargs := scope(branchCol, f.Branch)
	where += branch
	args = append(args, bargs...)
	switch f.Domain {
	case "tabungan":
		where += " AND product_id NOT IN ('RAK','TAB_INTERNAL')"
	case "deposito":
		if f.Category == "jatuh-tempo" {
			where += " AND maturity_date >= ?"
			args = append(args, f.Date)
		}
	}
	if f.Category == "dpk" || f.Category == "abp" {
		products := "'119','199'"
		if f.Domain == "deposito" {
			products = "'203','204'"
		}
		if f.Category == "abp" {
			where += " AND product_id IN (" + products + ")"
		} else {
			where += " AND product_id NOT IN (" + products + ")"
		}
	}
	if f.Bucket != "" {
		switch f.Bucket {
		case "0-7":
			where += " AND DATEDIFF(maturity_date, ?) BETWEEN 0 AND 7"
		case "8-30":
			where += " AND DATEDIFF(maturity_date, ?) BETWEEN 8 AND 30"
		case "31+":
			where += " AND DATEDIFF(maturity_date, ?) > 30"
		}
		args = append(args, f.Date)
	}
	if f.Metric == "booking" {
		amount = money("pokok_pinjaman")
		where += " AND STR_TO_DATE(periode_mulai,'%d/%m/%Y') BETWEEN ? AND ?"
		args = append(args, domain.PeriodStart(f.Filter), f.Date)
	}
	if f.Metric == "noa" {
		amount = "1"
	}
	sum := fmt.Sprintf("SELECT COUNT(*),CAST(ROUND(COALESCE(SUM(%s),0),0) AS SIGNED) FROM %s WHERE %s", amount, table, where)
	if err := q.tx.QueryRowContext(ctx, sum, args...).Scan(&res.Count, &res.Total); err != nil {
		return res, err
	}
	search := strings.TrimSpace(f.Search)
	filteredWhere := where
	filteredArgs := append([]any{}, args...)
	if search != "" {
		filteredWhere += fmt.Sprintf(" AND CONCAT_WS(' ',%s,%s,%s,%s) LIKE ?", nameCol, accountCol, cifCol, branchCol)
		filteredArgs = append(filteredArgs, "%"+search+"%")
	}
	filteredSum := fmt.Sprintf("SELECT COUNT(*),CAST(ROUND(COALESCE(SUM(%s),0),0) AS SIGNED) FROM %s WHERE %s", amount, table, filteredWhere)
	if err := q.tx.QueryRowContext(ctx, filteredSum, filteredArgs...).Scan(&res.FilterCount, &res.Filtered); err != nil {
		return res, err
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
	query := fmt.Sprintf(`SELECT COALESCE(%s,''),COALESCE(%s,''),COALESCE(%s,''),LEFT(%s,3),COALESCE(%s,''),COALESCE(%s,''),
		CAST(ROUND(COALESCE(%s,0),0) AS SIGNED),CAST(ROUND(COALESCE(%s,0),0) AS SIGNED),%s
		FROM %s WHERE %s ORDER BY %s LIMIT ? OFFSET ?`, nameCol, accountCol, cifCol, branchCol, productCol, collectCol, amount, money(map[string]string{"tabungan": "credit_balance", "deposito": "nominal", "kredit": "sisa_pokok_pinjaman"}[f.Domain]), due, table, filteredWhere, accountCol)
	filteredArgs = append(filteredArgs, size, (f.Page-1)*size)
	rows, err := q.tx.QueryContext(ctx, query, filteredArgs...)
	if err != nil {
		return res, err
	}
	defer rows.Close()
	for rows.Next() {
		var row domain.Record
		var date sql.NullTime
		if err := rows.Scan(&row.Name, &row.Account, &row.CIF, &row.Branch, &row.Product, &row.Collectibility, &row.Amount, &row.Outstanding, &date); err != nil {
			return res, err
		}
		if date.Valid {
			row.Due = date.Time
		}
		res.Rows = append(res.Rows, row)
	}
	return res, rows.Err()
}
