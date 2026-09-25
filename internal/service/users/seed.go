package users

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
)

type SeedResult struct{ Created, Skipped int }

// SeedCSV reads credentials only from the supplied stream. Existing accounts are never changed.
type seedStore interface {
	ByUsername(context.Context, string) (User, error)
	Save(context.Context, User, string) (int64, error)
}

func (s *Store) SeedCSV(ctx context.Context, input io.Reader) (SeedResult, error) {
	return seedCSV(ctx, input, s)
}
func seedCSV(ctx context.Context, input io.Reader, s seedStore) (SeedResult, error) {
	r := csv.NewReader(input)
	r.Comma = ';'
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		return SeedResult{}, err
	}
	if len(header) > 0 {
		header[0] = strings.TrimPrefix(header[0], "\ufeff")
	}
	want := []string{"Username", "Password", "Cabang", "Nama karyawan", "Nama jabatan"}
	if len(header) != len(want) {
		return SeedResult{}, errors.New("invalid seed CSV header")
	}
	for i, v := range want {
		if strings.TrimSpace(header[i]) != v {
			return SeedResult{}, errors.New("invalid seed CSV header")
		}
	}
	var result SeedResult
	for line := 2; ; line++ {
		row, e := r.Read()
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			return result, fmt.Errorf("CSV row %d: %w", line, e)
		}
		if len(row) != 5 {
			return result, fmt.Errorf("CSV row %d: expected five columns", line)
		}
		u := User{Username: strings.TrimSpace(row[0]), BranchCode: strings.TrimSpace(row[2]), FullName: strings.TrimSpace(row[3]), Position: strings.TrimSpace(row[4]), Role: "USER", IsActive: true}
		if e := Validate(u, row[1], true); e != nil {
			return result, fmt.Errorf("CSV row %d: %w", line, e)
		}
		_, e = s.ByUsername(ctx, u.Username)
		if e == nil {
			result.Skipped++
			continue
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return result, e
		}
		if _, e = s.Save(ctx, u, row[1]); e != nil {
			if errors.Is(e, ErrDuplicate) {
				result.Skipped++
				continue
			}
			return result, fmt.Errorf("CSV row %d: %w", line, e)
		}
		result.Created++
	}
	return result, nil
}
