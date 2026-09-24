package realtime

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxReportBytes = 64 << 20

type Fincloud struct {
	BaseURL, Username, Password, RoleID, LocationID string
	Client                                          *http.Client
}

func NewFincloud(baseURL, username, password, role, location string, insecureTLS bool) *Fincloud {
	return &Fincloud{BaseURL: strings.TrimRight(baseURL, "/"), Username: username, Password: password, RoleID: role, LocationID: location,
		Client: &http.Client{Timeout: 3 * time.Minute, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: insecureTLS}}}}
}

func (f *Fincloud) Fetch(ctx context.Context, date time.Time) (Dataset, error) {
	if f.BaseURL == "" || f.Username == "" || f.Password == "" || f.RoleID == "" || f.LocationID == "" {
		return Dataset{}, errors.New("Fincloud read-only report credentials are incomplete")
	}
	if !strings.HasPrefix(f.BaseURL, "https://") {
		return Dataset{}, errors.New("Fincloud base URL must use HTTPS")
	}
	session, err := f.login(ctx)
	if err != nil {
		return Dataset{}, err
	}
	d := Dataset{Date: date}
	for _, report := range []struct {
		name  string
		parse func([]byte) error
	}{
		{"Savings Balance Details Report Today", func(b []byte) error { var e error; d.Savings, e = parseSavings(b); return e }},
		{"Time Deposit Account Balance Detail Today", func(b []byte) error { var e error; d.Deposits, e = parseDeposits(b, date); return e }},
		{"Loan Outstanding Details Report Today", func(b []byte) error { var e error; d.Loans, e = parseLoans(b, date); return e }},
	} {
		params := []string{""}
		body, e := f.download(ctx, session, report.name, params)
		if errors.Is(e, errSessionExpired) {
			session, e = f.login(ctx)
			if e == nil {
				body, e = f.download(ctx, session, report.name, params)
			}
		}
		if e != nil {
			return Dataset{}, fmt.Errorf("Fincloud %s download failed: %w", report.name, e)
		}
		if e = report.parse(body); e != nil {
			return Dataset{}, fmt.Errorf("Fincloud %s invalid: %w", report.name, e)
		}
	}
	for branchID := 0; branchID <= 8; branchID++ {
		wanted := fmt.Sprintf("%03d", branchID)
		params := []string{wanted, date.Format("2006-1-2")}
		body, e := f.download(ctx, session, "Balance Sheet Report csv", params)
		if errors.Is(e, errSessionExpired) {
			session, e = f.login(ctx)
			if e == nil {
				body, e = f.download(ctx, session, "Balance Sheet Report csv", params)
			}
		}
		if e != nil {
			return Dataset{}, fmt.Errorf("Fincloud Balance Sheet branch %s download failed: %w", wanted, e)
		}
		rows, e := parseBalance(body, wanted)
		if e != nil {
			return Dataset{}, fmt.Errorf("Fincloud Balance Sheet branch %s invalid: %w", wanted, e)
		}
		d.Balance = append(d.Balance, rows...)
	}
	if err := validate(d); err != nil {
		return Dataset{}, err
	}
	return d, nil
}

var errSessionExpired = errors.New("Fincloud session expired")

func (f *Fincloud) login(ctx context.Context) (string, error) {
	form := url.Values{"locationid": {f.LocationID}, "roleid": {f.RoleID}, "username": {f.Username}, "pwd": {f.Password}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.BaseURL+"/admin/access/login", strings.NewReader(form.Encode()))
	if err != nil {
		return "", errors.New("constructing Fincloud login failed")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := f.Client.Do(req)
	if err != nil {
		return "", errors.New("Fincloud login connection failed " + err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errors.New("Fincloud login rejected")
	}
	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Result struct {
				SessionID string `json:"sessionid"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil || payload.Status != "ok" || payload.Data.Result.SessionID == "" {
		return "", errors.New("Fincloud login response invalid")
	}
	return payload.Data.Result.SessionID, nil
}

func (f *Fincloud) download(ctx context.Context, session, name string, params []string) ([]byte, error) {
	p, _ := json.Marshal(params)
	q := url.Values{"nm": {name}, "type": {"csv"}, "p": {string(p)}}
	form := url.Values{"sessionId": {session}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.BaseURL+"/system/laporanUmum/data/lap?"+q.Encode(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, errors.New("constructing Fincloud report request failed")
	}
	req.Header.Set("sessionid", session)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:142.0) Gecko/20100101 Firefox/142.0")
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, errors.New("Fincloud report connection failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, errSessionExpired
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Fincloud report HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxReportBytes+1))
	if err != nil {
		return nil, errors.New("reading Fincloud report failed")
	}
	if len(data) > maxReportBytes {
		return nil, errors.New("Fincloud report exceeds size limit")
	}
	return data, nil
}

func readCSV(body []byte, required []string, visit func(map[string]string) error) error {
	body = bytes.TrimPrefix(body, []byte{0xEF, 0xBB, 0xBF})
	r := csv.NewReader(bytes.NewReader(body))
	r.Comma = '|'
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	r.LazyQuotes = true
	header, err := r.Read()
	if err != nil {
		return errors.New("missing CSV header")
	}
	index := map[string]int{}
	for i, h := range header {
		key := canonical(h)
		if _, ok := index[key]; ok {
			return fmt.Errorf("duplicate CSV column %s", key)
		}
		index[key] = i
	}
	for _, field := range required {
		if _, ok := index[field]; !ok {
			return fmt.Errorf("missing CSV column %s", field)
		}
	}
	count := 0
	for line := 2; ; line++ {
		cells, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("invalid CSV line %d", line)
		}
		blank := true
		for _, cell := range cells {
			if strings.TrimSpace(cell) != "" {
				blank = false
				break
			}
		}
		if blank {
			continue
		}
		if len(cells) != len(header) {
			return fmt.Errorf("CSV line %d has wrong column count", line)
		}
		row := make(map[string]string, len(required))
		for field, i := range index {
			row[field] = strings.TrimSpace(cells[i])
		}
		if err := visit(row); err != nil {
			return fmt.Errorf("CSV line %d: %w", line, err)
		}
		count++
	}
	if count == 0 {
		return errors.New("CSV has no data rows")
	}
	return nil
}

func canonical(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), "_", " ")), "_")
}

func branch(s string) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) < 3 || s[:2] != "00" || s[2] < '0' || s[2] > '8' {
		return "", errors.New("invalid branch code")
	}
	return s[:3], nil
}

func parseDate(s string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02", "2006-1-2", "02/01/2006", "02-01-2006", "20060102"} {
		if d, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			return d, nil
		}
	}
	return time.Time{}, errors.New("invalid date")
}

func normalizeMoney(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	negative := false
	if len(s) > 2 && ((s[0] == '<' && s[len(s)-1] == '>') || (s[0] == '(' && s[len(s)-1] == ')')) {
		negative = true
		s = s[1 : len(s)-1]
	}
	if strings.HasPrefix(s, "-") {
		negative = !negative
		s = s[1:]
	}
	s = strings.ReplaceAll(s, " ", "")
	comma, dot := strings.LastIndex(s, ","), strings.LastIndex(s, ".")
	switch {
	case comma >= 0 && dot >= 0 && comma > dot:
		s = strings.ReplaceAll(strings.ReplaceAll(s, ".", ""), ",", ".")
	case comma >= 0 && dot >= 0:
		s = strings.ReplaceAll(s, ",", "")
	case comma >= 0:
		parts := strings.Split(s, ",")
		grouped := len(parts) > 1 && len(parts[0]) >= 1 && len(parts[0]) <= 3
		for _, p := range parts[1:] {
			if len(p) != 3 {
				grouped = false
			}
		}
		if grouped {
			s = strings.ReplaceAll(s, ",", "")
		} else if len(parts) == 2 {
			s = parts[0] + "." + parts[1]
		} else {
			return "", errors.New("invalid amount grouping")
		}
	case dot >= 0:
		parts := strings.Split(s, ".")
		grouped := len(parts) > 1 && len(parts[0]) >= 1 && len(parts[0]) <= 3
		for _, p := range parts[1:] {
			if len(p) != 3 {
				grouped = false
			}
		}
		if grouped {
			s = strings.ReplaceAll(s, ".", "")
		} else if len(parts) != 2 {
			return "", errors.New("invalid amount grouping")
		}
	}
	parts := strings.Split(s, ".")
	if len(parts) > 2 || len(parts[0]) == 0 || len(parts[0]) > 16 {
		return "", errors.New("invalid amount")
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return "", errors.New("invalid amount")
	}
	frac := int64(0)
	if len(parts) == 2 {
		if len(parts[1]) == 0 || len(parts[1]) > 2 {
			return "", errors.New("invalid amount precision")
		}
		if len(parts[1]) == 1 {
			parts[1] += "0"
		}
		frac, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return "", errors.New("invalid amount")
		}
	}
	if whole > 92233720368547758 {
		return "", errors.New("amount overflow")
	}
	cents := whole*100 + frac
	if negative {
		return fmt.Sprintf("-%d.%02d", cents/100, cents%100), nil
	}
	return fmt.Sprintf("%d.%02d", cents/100, cents%100), nil
}

func parseSavings(body []byte) ([]Saving, error) {
	var out []Saving
	err := readCSV(body, []string{"branch_code", "product_id", "account_no", "customer_name", "cif_no", "credit_balance"}, func(row map[string]string) error {
		b, e := branch(row["branch_code"])
		if e != nil {
			return e
		}
		amount, e := normalizeMoney(row["credit_balance"])
		if e != nil {
			return e
		}
		out = append(out, Saving{b, row["product_id"], row["account_no"], row["customer_name"], row["cif_no"], amount})
		return nil
	})
	return out, err
}

func parseDeposits(body []byte, date time.Time) ([]Deposit, error) {
	var out []Deposit
	err := readCSV(body, []string{"date", "branch_code", "product_id", "account_no", "customer_name", "cif_no", "nominal", "maturity_date"}, func(row map[string]string) error {
		asOf, e := parseDate(row["date"])
		if e != nil || !asOf.Equal(date) {
			return errors.New("deposit business date differs from snapshot date")
		}
		b, e := branch(row["branch_code"])
		if e != nil {
			return e
		}
		amount, e := normalizeMoney(row["nominal"])
		if e != nil {
			return e
		}
		maturity, e := parseDate(row["maturity_date"])
		if e != nil {
			return e
		}
		out = append(out, Deposit{b, row["product_id"], row["account_no"], row["customer_name"], row["cif_no"], amount, maturity})
		return nil
	})
	return out, err
}

func parseLoans(body []byte, date time.Time) ([]Loan, error) {
	var out []Loan
	err := readCSV(body, []string{"date_params", "branch_code", "product_id", "loan_account_no", "customer_name", "cif_no", "loan_principal", "loan_outstanding", "bi_collectability"}, func(row map[string]string) error {
		asOf, e := parseDate(row["date_params"])
		if e != nil || !asOf.Equal(date) {
			return errors.New("loan business date differs from snapshot date")
		}
		b, e := branch(row["branch_code"])
		if e != nil {
			return e
		}
		startText := row["start_date"]
		if startText == "" {
			startText = row["periode_mulai"]
		}
		start, e := parseDate(startText)
		if e != nil {
			return errors.New("loan start date missing or invalid")
		}
		principal, e := normalizeMoney(row["loan_principal"])
		if e != nil {
			return e
		}
		outstanding, e := normalizeMoney(row["loan_outstanding"])
		if e != nil {
			return e
		}
		collect := row["bi_collectability"]
		if collect != "1" && collect != "2" && collect != "3" && collect != "4" && collect != "5" {
			return errors.New("invalid loan collectability")
		}
		out = append(out, Loan{b, row["product_id"], row["loan_account_no"], row["customer_name"], row["cif_no"], principal, outstanding, collect, start})
		return nil
	})
	return out, err
}

func parseBalance(body []byte, requestedBranch string) ([]Balance, error) {
	var out []Balance
	err := readCSV(body, []string{"coa_no", "chart_of_account", "last_balance"}, func(row map[string]string) error {
		// Fincloud's CSV Branch can differ from the requested branch; dwh-v2 uses the request scope.
		amount, e := normalizeMoney(row["last_balance"])
		if e != nil {
			return e
		}
		out = append(out, Balance{requestedBranch, row["coa_no"], row["chart_of_account"], amount})
		return nil
	})
	return out, err
}
