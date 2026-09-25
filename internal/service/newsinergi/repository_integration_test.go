package newsinergi

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ibldzn/dashboard-roro-jongrang/internal/domain"
)

// Run with NEWSINERGI_INTEGRATION_TEST=1 and DWH_DBSTRING set.
func TestLivePositionsMatchSignedApplications(t *testing.T) {
	if os.Getenv("NEWSINERGI_INTEGRATION_TEST") != "1" {
		t.Skip("live Newsinergi integration test is opt-in")
	}
	ctx := context.Background()
	r, err := Open(ctx, os.Getenv("DWH_DBSTRING"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var latest string
	if err := tx.QueryRowContext(ctx, `SELECT DATE_FORMAT(MAX(ca.tanggal_realisasi),'%Y-%m-%d') FROM credit_applications ca JOIN branches rb ON rb.id=ca.requestor_branch_id WHERE ca.status='pk_signed' AND rb.company_id=1`).Scan(&latest); err != nil {
		t.Fatal(err)
	}
	day, err := time.Parse("2006-01-02", latest)
	if err != nil {
		t.Fatal(err)
	}
	var unsigned int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM credit_applications WHERE status <> 'pk_signed'`).Scan(&unsigned); err != nil || unsigned == 0 {
		t.Fatalf("status exclusion could not be checked: count=%d err=%v", unsigned, err)
	}
	for _, mode := range []string{"daily", "monthly", "yearly"} {
		for branchID := 0; branchID <= 8; branchID++ {
			branch := "ALL"
			if branchID != 0 {
				branch = fmt.Sprintf("%03d", branchID)
			}
			f := domain.Filter{Mode: mode, Date: day, Branch: branch}
			got, err := r.Positions(ctx, []domain.Filter{f})
			if err != nil {
				t.Fatal(err)
			}
			var booking, plafond int64
			err = tx.QueryRowContext(ctx, `SELECT
				CAST(ROUND(COALESCE(SUM(CASE WHEN ca.tanggal_realisasi >= ? THEN ca.plafond_kredit ELSE 0 END),0),0) AS SIGNED),
				CAST(ROUND(COALESCE(SUM(ca.plafond_kredit),0),0) AS SIGNED)
				FROM credit_applications ca JOIN branches rb ON rb.id=ca.requestor_branch_id
				WHERE ca.status='pk_signed' AND rb.company_id=1 AND ca.tanggal_realisasi <= ?
				AND (?=0 OR rb.id=?)`, domain.PeriodStart(f), day, branchID, branchID).Scan(&booking, &plafond)
			if err != nil {
				t.Fatal(err)
			}
			p := got[day.Format("2006-01-02")]
			if !p.Exists || p.Booking != booking || p.Plafond != plafond {
				t.Errorf("%s %s: got %+v, direct SQL booking=%d plafond=%d", mode, branch, p, booking, plafond)
			}
		}
	}
}
