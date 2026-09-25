package users

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/ibldzn/dashboard-roro-jongrang/internal/domain"
	"golang.org/x/crypto/bcrypt"
)

const schema = `CREATE TABLE IF NOT EXISTS bm_users (
 id BIGINT AUTO_INCREMENT PRIMARY KEY,
 username VARCHAR(100) NOT NULL UNIQUE,
 password_hash VARCHAR(255) NOT NULL,
 full_name VARCHAR(255) NOT NULL,
 position VARCHAR(255) NOT NULL,
 branch_code VARCHAR(3) NOT NULL DEFAULT '',
 role VARCHAR(5) NOT NULL,
 is_active BOOLEAN NOT NULL DEFAULT TRUE,
 created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
 updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6)
)`

type User struct {
	ID                                             int64
	Username, FullName, Position, BranchCode, Role string
	IsActive                                       bool
	CreatedAt, UpdatedAt                           time.Time
	passwordHash                                   string
}

func (u User) Branch() string {
	if u.Role == "ADMIN" {
		return "ALL"
	}
	return u.BranchCode
}
func (u *User) SetPassword(password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	u.passwordHash = string(hash)
	return nil
}

func (u *User) KeepPassword(other User) { u.passwordHash = other.passwordHash }
func (u User) CheckPassword(password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(u.passwordHash), []byte(password)) == nil
}

type Store struct{ DB *sql.DB }

func Open(ctx context.Context, dsn string) (*Store, error) {
	if dsn == "" {
		return nil, errors.New("APP_DBSTRING is required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, errors.New("invalid APP_DBSTRING")
	}
	if cfg.DBName == "" || strings.EqualFold(cfg.DBName, "dwhv2") || strings.EqualFold(cfg.DBName, "newsinergi") {
		return nil, errors.New("APP_DBSTRING must name a separate local application database")
	}
	cfg.ParseTime = true
	cfg.MultiStatements = false
	cfg.Timeout = 5 * time.Second
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, errors.New("application database unavailable")
	}
	s, err := New(ctx, db)
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func New(ctx context.Context, db *sql.DB) (*Store, error) {
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("create BM users table: %w", err)
	}
	return &Store{DB: db}, nil
}

const columns = `id,username,password_hash,full_name,position,branch_code,role,is_active,created_at,updated_at`

func scan(s interface{ Scan(...any) error }) (User, error) {
	var u User
	err := s.Scan(&u.ID, &u.Username, &u.passwordHash, &u.FullName, &u.Position, &u.BranchCode, &u.Role, &u.IsActive, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}
func (s *Store) ByUsername(ctx context.Context, username string) (User, error) {
	return scan(s.DB.QueryRowContext(ctx, `SELECT `+columns+` FROM bm_users WHERE username=?`, username))
}
func (s *Store) ByID(ctx context.Context, id int64) (User, error) {
	return scan(s.DB.QueryRowContext(ctx, `SELECT `+columns+` FROM bm_users WHERE id=?`, id))
}
func (s *Store) List(ctx context.Context) ([]User, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+columns+` FROM bm_users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, e := scan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

var ErrDuplicate = errors.New("username already exists")

func Validate(u User, password string, creating bool) error {
	u.Username = strings.TrimSpace(u.Username)
	if u.Username == "" || len(u.Username) > 100 {
		return errors.New("Username wajib diisi (maksimal 100 karakter)")
	}
	if strings.TrimSpace(u.FullName) == "" || strings.TrimSpace(u.Position) == "" {
		return errors.New("Nama dan jabatan wajib diisi")
	}
	if u.Role != "ADMIN" && u.Role != "USER" {
		return errors.New("Role tidak valid")
	}
	if u.Role == "USER" {
		if u.BranchCode == "ALL" || u.BranchCode == "" {
			return errors.New("Cabang tidak valid")
		}
		if _, ok := domain.BranchLabel(u.BranchCode); !ok {
			return errors.New("Cabang tidak valid")
		}
	} else if u.BranchCode != "" {
		if _, ok := domain.BranchLabel(u.BranchCode); !ok || u.BranchCode == "ALL" {
			return errors.New("Cabang tidak valid")
		}
	}
	if creating && password == "" {
		return errors.New("Password wajib diisi")
	}
	if password != "" && (len(password) < 8 || len(password) > 72) {
		return errors.New("Password harus 8–72 karakter")
	}
	return nil
}
func (s *Store) Save(ctx context.Context, u User, password string) (int64, error) {
	if err := Validate(u, password, u.ID == 0); err != nil {
		return 0, err
	}
	u.Username = strings.TrimSpace(u.Username)
	var hash []byte
	var err error
	if password != "" {
		hash, err = bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return 0, err
		}
	}
	if u.ID == 0 {
		result, e := s.DB.ExecContext(ctx, `INSERT INTO bm_users (username,password_hash,full_name,position,branch_code,role,is_active) VALUES (?,?,?,?,?,?,?)`, u.Username, string(hash), u.FullName, u.Position, u.BranchCode, u.Role, u.IsActive)
		if e != nil {
			return 0, duplicate(e)
		}
		return result.LastInsertId()
	}
	query := `UPDATE bm_users SET username=?,full_name=?,position=?,branch_code=?,role=?,is_active=?`
	args := []any{u.Username, u.FullName, u.Position, u.BranchCode, u.Role, u.IsActive}
	if password != "" {
		query += `,password_hash=?`
		args = append(args, string(hash))
	}
	query += ` WHERE id=?`
	args = append(args, u.ID)
	result, e := s.DB.ExecContext(ctx, query, args...)
	if e != nil {
		return 0, duplicate(e)
	}
	n, e := result.RowsAffected()
	if e != nil {
		return 0, e
	}
	if n == 0 {
		return 0, sql.ErrNoRows
	}
	return u.ID, nil
}
func duplicate(err error) error {
	var m *mysql.MySQLError
	if errors.As(err, &m) && m.Number == 1062 {
		return ErrDuplicate
	}
	return err
}

func (s *Store) BootstrapAdmin(ctx context.Context, username, password, name string) (bool, error) {
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM bm_users WHERE role='ADMIN'`).Scan(&count); err != nil {
		return false, err
	}
	if count > 0 {
		return false, nil
	}
	if username == "" || password == "" || name == "" {
		return false, errors.New("BM_ADMIN_USERNAME, BM_ADMIN_PASSWORD, and BM_ADMIN_NAME are required")
	}
	_, err := s.Save(ctx, User{Username: username, FullName: name, Position: "Administrator", Role: "ADMIN", IsActive: true}, password)
	return err == nil, err
}
