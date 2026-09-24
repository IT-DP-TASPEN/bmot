package realtime

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

// This test writes only to a dedicated local roro_test database, never the DWH.
func TestLocalSnapshotAtomicPublish(t *testing.T) {
	if os.Getenv("APP_INTEGRATION_TEST") != "1" {
		t.Skip("local application DB integration test is opt-in")
	}
	dsn := os.Getenv("APP_DBSTRING")
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || cfg.DBName != "roro_test" {
		t.Fatal("APP_DBSTRING must target roro_test")
	}
	ctx := context.Background()
	store, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	day := Today()
	dataset := func(suffix string) Dataset {
		return Dataset{Date: day,
			Savings:  []Saving{{Branch: "001", Product: "119", Account: "S" + suffix, Customer: "A", CIF: "C1", Balance: "100.00"}},
			Deposits: []Deposit{{Branch: "001", Product: "203", Account: "D" + suffix, Customer: "B", CIF: "C2", Nominal: "200.00", Maturity: day.AddDate(0, 0, 7)}},
			Loans:    []Loan{{Branch: "001", Product: "P1", Account: "L" + suffix, Customer: "C", CIF: "C3", Principal: "300.00", Outstanding: "250.00", Collectibility: "3", Start: day}},
			Balance:  []Balance{{Branch: "001", COA: "1", Name: "Assets", Amount: "550.00"}},
		}
	}
	var ids []int64
	defer func() {
		for _, id := range ids {
			store.db.ExecContext(ctx, `DELETE FROM dashboard_snapshot_runs WHERE id=?`, id)
		}
	}()
	first, err := store.Start(ctx, day)
	if err != nil {
		t.Fatal(err)
	}
	ids = append(ids, first)
	if err := store.Publish(ctx, first, dataset("1")); err != nil {
		t.Fatal(err)
	}
	second, err := store.Start(ctx, day)
	if err != nil {
		t.Fatal(err)
	}
	ids = append(ids, second)
	if _, err := store.db.ExecContext(ctx, `INSERT INTO dashboard_rt_savings (snapshot_id,as_of_date,branch,product_id,account_no,customer_name,cif_no,credit_balance) VALUES (?,?,?,?,?,?,?,?)`, second, day, "001", "119", "LOADING", "A", "C1", "999.00"); err != nil {
		t.Fatal(err)
	}
	var account string
	query := `SELECT account_no FROM dashboard_rt_savings WHERE snapshot_id=(SELECT id FROM dashboard_snapshot_runs WHERE status='published' ORDER BY id DESC LIMIT 1)`
	if err := store.db.QueryRowContext(ctx, query).Scan(&account); err != nil || account != "S1" {
		t.Fatalf("loading generation visible: account=%q err=%v", account, err)
	}
	bad := dataset("2")
	bad.Deposits = nil
	if err := store.Publish(ctx, second, bad); err == nil {
		t.Fatal("incomplete generation published")
	}
	if err := store.Fail(ctx, second, "test failure"); err != nil {
		t.Fatal(err)
	}
	p, err := store.Latest(ctx)
	if err != nil || p == nil || p.ID != first {
		t.Fatalf("previous published generation lost: %+v %v", p, err)
	}
	third, err := store.Start(ctx, day)
	if err != nil {
		t.Fatal(err)
	}
	ids = append(ids, third)
	if err := store.Publish(ctx, third, dataset("3")); err != nil {
		t.Fatal(err)
	}
	p, err = store.Latest(ctx)
	if err != nil || p == nil || p.ID != third {
		t.Fatalf("new generation not published: %+v %v", p, err)
	}
	if err := store.db.QueryRowContext(ctx, query).Scan(&account); err != nil || account != "S3" {
		t.Fatalf("published rows wrong: account=%q err=%v", account, err)
	}
	if p.PublishedAt.Before(time.Now().Add(-time.Minute)) {
		t.Fatal("published timestamp too old")
	}
}
