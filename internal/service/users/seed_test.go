package users

import (
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"strings"
	"testing"
)

type memorySeed struct{ users map[string]User }

func (m *memorySeed) ByUsername(_ context.Context, name string) (User, error) {
	u, ok := m.users[name]
	if !ok {
		return User{}, sql.ErrNoRows
	}
	return u, nil
}
func (m *memorySeed) Save(_ context.Context, u User, password string) (int64, error) {
	if _, ok := m.users[u.Username]; ok {
		return 0, ErrDuplicate
	}
	if err := Validate(u, password, true); err != nil {
		return 0, err
	}
	if err := u.SetPassword(password); err != nil {
		return 0, err
	}
	u.ID = int64(len(m.users) + 1)
	m.users[u.Username] = u
	return u.ID, nil
}
func TestSeedCSV(t *testing.T) {
	var fixture strings.Builder
	w := csv.NewWriter(&fixture)
	w.Comma = ';'
	_ = w.Write([]string{"Username", "Password", "Cabang", "Nama karyawan", "Nama jabatan"})
	passwords := map[string]string{}
	for branch := 1; branch <= 8; branch++ {
		for job := 1; job <= 2; job++ {
			username := fmt.Sprintf("%07d", branch*100+job)
			if branch == 3 && job == 1 {
				username = "0985031"
			}
			password := fmt.Sprintf("test-password-%d-%d", branch, job)
			passwords[username] = password
			position := "Branch Manager"
			if job == 2 {
				position = "Assistant of Branch Manager"
			}
			_ = w.Write([]string{username, password, fmt.Sprintf("%03d", branch), "Test Employee", position})
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		t.Fatal(err)
	}
	m := &memorySeed{users: map[string]User{}}
	run := func() SeedResult {
		result, err := seedCSV(context.Background(), strings.NewReader(fixture.String()), m)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := run()
	if first.Created != 16 || first.Skipped != 0 {
		t.Fatalf("first seed: %+v", first)
	}
	second := run()
	if second.Created != 0 || second.Skipped != 16 || len(m.users) != 16 {
		t.Fatalf("second seed: %+v, users=%d", second, len(m.users))
	}
	if _, ok := m.users["0985031"]; !ok {
		t.Fatal("leading zero lost")
	}
	counts := map[string]int{}
	for _, u := range m.users {
		if u.Role != "USER" || !u.IsActive || u.passwordHash == passwords[u.Username] || !u.CheckPassword(passwords[u.Username]) {
			t.Fatalf("invalid seed user %s", u.Username)
		}
		counts[u.BranchCode]++
	}
	for i := 1; i <= 8; i++ {
		code := fmt.Sprintf("%03d", i)
		if counts[code] != 2 {
			t.Fatalf("branch %s: %d", code, counts[code])
		}
	}
}
