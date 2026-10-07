package app

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"modernc.org/sqlite"
)

const timestampFormat = "2006-01-02T15:04:05.000000000Z"

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("unicode_lower", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		value, ok := args[0].(string)
		if !ok {
			return "", nil
		}
		return strings.ToLower(value), nil
	})
}

type Store struct{ db *sql.DB }
type User struct {
	ID        int64     `json:"id"`
	FullName  string    `json:"full_name"`
	CardUID   string    `json:"card_uid"`
	IsActive  bool      `json:"is_active"`
	CreatedAt string    `json:"created_at"`
	Encoding  []float64 `json:"-"`
}
type Entry struct {
	ID         int64  `json:"id"`
	Timestamp  string `json:"timestamp"`
	CardUID    string `json:"card_uid"`
	FullName   string `json:"full_name"`
	Result     string `json:"result"`
	DurationMS int64  `json:"duration_ms"`
}

func OpenStore(path string) (*Store, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;
	CREATE TABLE IF NOT EXISTS users (
	 id INTEGER PRIMARY KEY AUTOINCREMENT,
	 full_name TEXT NOT NULL,
	 card_uid TEXT NOT NULL UNIQUE CHECK(length(card_uid)=14),
	 face_encoding TEXT NOT NULL,
	 is_active INTEGER NOT NULL DEFAULT 1 CHECK(is_active IN (0,1)),
	 created_at TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS logs (
	 id INTEGER PRIMARY KEY AUTOINCREMENT,
	 timestamp TEXT NOT NULL,
	 card_uid TEXT NOT NULL,
	 full_name TEXT NOT NULL DEFAULT '',
	 result TEXT NOT NULL,
	 duration_ms INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX IF NOT EXISTS idx_logs_time ON logs(timestamp);
	CREATE INDEX IF NOT EXISTS idx_logs_uid ON logs(card_uid);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	var encoding string
	err := row.Scan(&u.ID, &u.FullName, &u.CardUID, &encoding, &u.IsActive, &u.CreatedAt)
	if err != nil {
		return u, err
	}
	err = json.Unmarshal([]byte(encoding), &u.Encoding)
	return u, err
}

const userColumns = "id,full_name,card_uid,face_encoding,is_active,created_at"

func (s *Store) UserByUID(ctx context.Context, uid string) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE card_uid=?", uid))
}
func (s *Store) Users(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+userColumns+" FROM users ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}
func (s *Store) CreateUser(ctx context.Context, name, uid string, encoding []float64) (User, error) {
	encoded, err := json.Marshal(encoding)
	if err != nil {
		return User{}, err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO users(full_name,card_uid,face_encoding,created_at) VALUES(?,?,?,?)", name, uid, string(encoded), time.Now().UTC().Format(timestampFormat))
	if err != nil {
		return User{}, err
	}
	return s.UserByUID(ctx, uid)
}
func (s *Store) SetActive(ctx context.Context, id int64, active bool) error {
	r, err := s.db.ExecContext(ctx, "UPDATE users SET is_active=? WHERE id=?", active, id)
	return affected(r, err)
}
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	r, err := s.db.ExecContext(ctx, "DELETE FROM users WHERE id=?", id)
	return affected(r, err)
}
func affected(r sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}

// Recheck the card in the same transaction as the successful audit entry.
// A deletion, block or replacement during inference must never grant access.
func (s *Store) RecordDecision(ctx context.Context, user User, uid, status string, duration int64) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if status == "success" {
		var id int64
		var active bool
		err = tx.QueryRowContext(ctx, "SELECT id,is_active FROM users WHERE card_uid=?", uid).Scan(&id, &active)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && id != user.ID) {
			status = "unknown_uid"
		} else if err != nil {
			return "", err
		} else if !active {
			status = "inactive_user"
		}
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO logs(timestamp,card_uid,full_name,result,duration_ms) VALUES(?,?,?,?,?)", time.Now().UTC().Format(timestampFormat), uid, user.FullName, status, duration)
	if err != nil {
		return "", err
	}
	return status, tx.Commit()
}

type LogFilter struct {
	Query, Status, Date string
	Limit, Offset       int
}
type LogPage struct {
	Items []Entry `json:"items"`
	Total int     `json:"total"`
}

func filterSQL(f LogFilter) (string, []any) {
	where := " WHERE 1=1"
	args := []any{}
	if f.Query != "" {
		where += " AND (instr(unicode_lower(full_name),unicode_lower(?))>0 OR instr(card_uid,?)>0)"
		args = append(args, f.Query, strings.ToUpper(f.Query))
	}
	if f.Status == "denied" {
		where += " AND result!='success'"
	} else if f.Status != "" {
		where += " AND result=?"
		args = append(args, f.Status)
	}
	if f.Date != "" {
		d, _ := time.ParseInLocation("2006-01-02", f.Date, moscow)
		where += " AND timestamp>=? AND timestamp<?"
		args = append(args, d.UTC().Format(timestampFormat), d.AddDate(0, 0, 1).UTC().Format(timestampFormat))
	}
	return where, args
}
func (s *Store) Logs(ctx context.Context, f LogFilter) (LogPage, error) {
	p := LogPage{Items: []Entry{}}
	where, args := filterSQL(f)
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM logs"+where, args...).Scan(&p.Total); err != nil {
		return p, err
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := s.db.QueryContext(ctx, "SELECT id,timestamp,card_uid,full_name,result,duration_ms FROM logs"+where+" ORDER BY id DESC LIMIT ? OFFSET ?", args...)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.Timestamp, &e.CardUID, &e.FullName, &e.Result, &e.DurationMS); err != nil {
			return p, err
		}
		p.Items = append(p.Items, e)
	}
	return p, rows.Err()
}

type Stats struct {
	Users   int    `json:"users"`
	Active  int    `json:"active"`
	Success int    `json:"success"`
	Denied  int    `json:"denied"`
	Date    string `json:"date"`
	Hourly  []int  `json:"hourly"`
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	now := time.Now().In(moscow)
	st := Stats{Date: now.Format("2006-01-02"), Hourly: make([]int, 24)}
	if err := s.db.QueryRowContext(ctx, "SELECT count(*),coalesce(sum(is_active),0) FROM users").Scan(&st.Users, &st.Active); err != nil {
		return st, err
	}
	where, args := filterSQL(LogFilter{Date: st.Date})
	rows, err := s.db.QueryContext(ctx, "SELECT timestamp,result FROM logs"+where, args...)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var ts, status string
		if err := rows.Scan(&ts, &status); err != nil {
			return st, err
		}
		if status == "success" {
			st.Success++
		} else {
			st.Denied++
		}
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			st.Hourly[t.In(moscow).Hour()]++
		}
	}
	return st, rows.Err()
}
