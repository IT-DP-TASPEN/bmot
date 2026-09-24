package dwh

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/ibldzn/dashboard-roro-jongrang/internal/service/realtime"
)

// This test writes only to the dedicated local roro_test database.
func TestLocalSnapshotUsesDashboardCalculations(t *testing.T) {
	if os.Getenv("APP_INTEGRATION_TEST") != "1" {
		t.Skip("local application DB integration test is opt-in")
	}
	dsn := os.Getenv("APP_DBSTRING")
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || cfg.DBName != "roro_test" {
		t.Fatal("APP_DBSTRING must target roro_test")
	}
	ctx := context.Background()
	store, err := realtime.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	day := realtime.Today()
	id, err := store.Start(ctx, day)
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB().ExecContext(ctx, `DELETE FROM dashboard_snapshot_runs WHERE id=?`, id)
	dataset := realtime.Dataset{
		Date: day,
		Savings: []realtime.Saving{
			{Branch: "001", Product: "119", Account: "S1", Balance: "100.00"},
			{Branch: "001", Product: "RAK", Account: "S2", Balance: "900.00"},
		},
		Deposits: []realtime.Deposit{{Branch: "001", Product: "203", Account: "D1", Nominal: "200.00", Maturity: day.AddDate(0, 0, 7)}},
		Loans:    []realtime.Loan{{Branch: "001", Product: "P1", Account: "L1", Principal: "300.00", Outstanding: "250.00", Collectibility: "3", Start: day}},
		Balance:  []realtime.Balance{{Branch: "001", COA: "1", Amount: "550.00"}, {Branch: "001", COA: "221", Amount: "-200.00"}},
	}
	if err := store.Publish(ctx, id, dataset); err != nil {
		t.Fatal(err)
	}
	repo := NewSnapshotRepository(store.DB())
	var savings map[string]fundingPosition
	var deposits map[string]fundingPosition
	var loans map[string]loanPosition
	var balance map[string]balancePosition
	err = repo.read(ctx, func(q reader) error {
		var e error
		if savings, e = q.SavingsPosition(ctx, []time.Time{day}, "001"); e != nil {
			return e
		}
		if deposits, e = q.DepositPosition(ctx, []time.Time{day}, "001"); e != nil {
			return e
		}
		if loans, e = q.LoanPosition(ctx, []time.Time{day}, "001", "daily"); e != nil {
			return e
		}
		balance, e = q.BalanceSheetPosition(ctx, []time.Time{day}, "001")
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := savings[key(day)]; got.Total != 100 || got.ABP != 100 || got.NOA != 1 {
		t.Fatalf("savings=%+v", got)
	}
	if got := deposits[key(day)]; got.Total != 200 || got.ABP != 200 {
		t.Fatalf("deposits=%+v", got)
	}
	if got := loans[key(day)]; got.Outstanding != 250 || got.Bad != 250 || got.Booking != 300 {
		t.Fatalf("loans=%+v", got)
	}
	if got := balance[key(day)]; got["1"] != 550 || got["221"] != 200 {
		t.Fatalf("balance=%+v", got)
	}
}
