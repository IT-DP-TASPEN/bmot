package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestReportParsingAndValidation(t *testing.T) {
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	savings, err := parseSavings([]byte("branch_code|product_id|account_no|customer_name|cif_no|credit_balance\n001-Jakarta|119|S1|A|C1|1,234.50\n001-Jakarta|RAK|S2|B|C2|0.50\n"))
	if err != nil || len(savings) != 2 || savings[0].Balance != "1234.50" {
		t.Fatalf("savings=%+v err=%v", savings, err)
	}
	deposits, err := parseDeposits([]byte("date|branch_code|product_id|account_no|customer_name|cif_no|nominal|maturity_date\n2026-09-24|001-Jakarta|203|D1|C|C3|2.000,25|2026-10-01\n"), day)
	if err != nil || len(deposits) != 1 || deposits[0].Nominal != "2000.25" {
		t.Fatalf("deposits=%+v err=%v", deposits, err)
	}
	loans, err := parseLoans([]byte("Date Params|Branch Code|Product ID|Loan Account No|Customer Name|CIF No|Start Date|Loan Principal|Loan Outstanding|BI Collectability\n2026-09-24|001-Jakarta|P1|L1|D|C4|2026-09-01|1,000.00|900.00|3\n"), day)
	if err != nil || len(loans) != 1 || loans[0].Principal != "1000.00" || loans[0].Outstanding != "900.00" {
		t.Fatalf("loans=%+v err=%v", loans, err)
	}
	balance, err := parseBalance([]byte("Branch|CoA No|Chart of Account|Last Balance\n001-Jakarta|221|Savings|<1,000.50>\n"), "003")
	if err != nil || len(balance) != 1 || balance[0].Amount != "-1000.50" || balance[0].Branch != "003" {
		t.Fatalf("balance=%+v err=%v", balance, err)
	}
	d := Dataset{day, savings, deposits, loans, balance}
	if err := validate(d); err != nil {
		t.Fatal(err)
	}
	d.Loans[0].Start = day.AddDate(0, 0, 1)
	if err := validate(d); err == nil {
		t.Fatal("future loan start accepted")
	}
}

func TestMalformedReportsNeverPublish(t *testing.T) {
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	if _, err := parseDeposits([]byte("date|branch_code|product_id|account_no|customer_name|cif_no|nominal|maturity_date\n2026-09-23|001|203|D1|A|C1|1|2026-10-01\n"), day); err == nil {
		t.Fatal("wrong business date accepted")
	}
	if _, err := parseLoans([]byte("date_params|branch_code|product_id|loan_account_no|customer_name|cif_no|start_date|loan_principal|loan_outstanding|bi_collectability\n2026-09-24|001|P1|L1|A|C1|2026-09-01|1|BAD|3\n"), day); err == nil {
		t.Fatal("invalid amount accepted")
	}
}

func TestFincloudUsesReadOnlyReports(t *testing.T) {
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	counts := map[string]int{}
	client := NewFincloud("https://fincloud.example", "user", "password", "role", "000", false)
	client.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		result := func(code int, body string) (*http.Response, error) {
			return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		}
		if r.URL.Path == "/admin/access/login" {
			if r.Method != http.MethodPost {
				t.Errorf("login method %s", r.Method)
			}
			return result(200, `{"status":"ok","data":{"result":{"sessionid":"test-session"}}}`)
		}
		if r.URL.Path != "/system/laporanUmum/data/lap" || r.Method != http.MethodGet || r.Header.Get("sessionid") != "test-session" {
			t.Errorf("unexpected Fincloud request: %s %s", r.Method, r.URL.Path)
			return result(400, "unexpected request")
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "sessionId=test-session" {
			t.Errorf("missing report session form")
		}
		name := r.URL.Query().Get("nm")
		counts[name]++
		switch name {
		case "Savings Balance Details Report Today":
			return result(200, "branch_code|product_id|account_no|customer_name|cif_no|credit_balance\n001|119|S1|A|C1|100.00\n")
		case "Time Deposit Account Balance Detail Today":
			return result(200, "date|branch_code|product_id|account_no|customer_name|cif_no|nominal|maturity_date\n2026-09-24|001|203|D1|B|C2|200.00|2026-10-01\n")
		case "Loan Outstanding Details Report Today":
			return result(200, "Date Params|Branch Code|Product ID|Loan Account No|Customer Name|CIF No|Start Date|Loan Principal|Loan Outstanding|BI Collectability\n2026-09-24|001|P1|L1|C|C3|2026-09-01|300.00|250.00|3\n")
		case "Balance Sheet Report csv":
			var params []string
			if err := json.Unmarshal([]byte(r.URL.Query().Get("p")), &params); err != nil || len(params) != 2 || params[1] != "2026-9-24" {
				t.Errorf("balance parameters: %v %v", params, err)
			}
			return result(200, "Branch|CoA No|Chart of Account|Last Balance\n001|1|Assets|550.00\n")
		default:
			t.Errorf("unexpected report %q", name)
			return result(400, "unexpected report")
		}
	})}
	d, err := client.Fetch(context.Background(), day)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Savings) != 1 || len(d.Deposits) != 1 || len(d.Loans) != 1 || len(d.Balance) != 9 {
		t.Fatalf("wrong dataset sizes: %+v", d)
	}
	if counts["Savings Balance Details Report Today"] != 1 || counts["Time Deposit Account Balance Detail Today"] != 1 || counts["Loan Outstanding Details Report Today"] != 1 || counts["Balance Sheet Report csv"] != 9 {
		t.Fatalf("wrong report calls: %v", counts)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type fakeGenerationStore struct {
	published  int64
	next       int64
	failed     bool
	publishErr error
}

func (s *fakeGenerationStore) Start(context.Context, time.Time) (int64, error) {
	s.next++
	return s.next, nil
}
func (s *fakeGenerationStore) Publish(_ context.Context, id int64, _ Dataset) error {
	if s.publishErr != nil {
		return s.publishErr
	}
	s.published = id
	return nil
}
func (s *fakeGenerationStore) Fail(context.Context, int64, string) error { s.failed = true; return nil }
func (s *fakeGenerationStore) Retain(context.Context, int) error         { return nil }

type fakeFetcher struct {
	dataset Dataset
	err     error
}

func (f fakeFetcher) Fetch(context.Context, time.Time) (Dataset, error) { return f.dataset, f.err }

func TestFailedRefreshKeepsPreviousPublishedGeneration(t *testing.T) {
	store := &fakeGenerationStore{published: 1, next: 1}
	r := &Refresher{Store: store, Fetcher: fakeFetcher{err: errors.New("report unavailable")}, Retention: 3}
	if err := r.Run(context.Background()); err == nil || store.published != 1 || !store.failed {
		t.Fatalf("published=%d failed=%v err=%v", store.published, store.failed, err)
	}
}

func TestStaleSnapshot(t *testing.T) {
	day := Today()
	now := time.Now()
	r := &Refresher{StaleAfter: 2 * time.Hour}
	if r.Stale(&Published{Date: day, PublishedAt: now.Add(-time.Hour)}, now) {
		t.Fatal("fresh snapshot marked stale")
	}
	if !r.Stale(&Published{Date: day, PublishedAt: now.Add(-3 * time.Hour)}, now) {
		t.Fatal("old snapshot marked fresh")
	}
	if !r.Stale(&Published{Date: day.AddDate(0, 0, -1), PublishedAt: now}, now) {
		t.Fatal("wrong business date marked fresh")
	}
}
