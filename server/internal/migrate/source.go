package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// row is one row of the Django database, keyed by column name. Reading rows
// generically keeps the importer tolerant of Django columns it doesn't use
// and of optional tables.
type row map[string]any

type querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func queryRows(ctx context.Context, q querier, query string, args ...any) ([]row, error) {
	rs, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", firstLine(query), err)
	}
	defer rs.Close()
	cols, err := rs.Columns()
	if err != nil {
		return nil, err
	}
	var out []row
	for rs.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rs.Scan(ptrs...); err != nil {
			return nil, err
		}
		r := row{}
		for i, c := range cols {
			r[c] = vals[i]
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

func hasTable(ctx context.Context, conn *sql.DB, name string) bool {
	var n int
	err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n)
	return err == nil && n > 0
}

func (r row) str(k string) string {
	switch v := r[k].(type) {
	case nil:
		return ""
	case string:
		return v
	case []byte:
		return string(v)
	case time.Time:
		return v.UTC().Format(time.RFC3339)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	}
	return fmt.Sprint(r[k])
}

func (r row) null(k string) bool { return r[k] == nil }

func (r row) i64(k string) int64 {
	switch v := r[k].(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	case bool:
		if v {
			return 1
		}
		return 0
	case string:
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	case []byte:
		n, _ := strconv.ParseInt(string(v), 10, 64)
		return n
	}
	return 0
}

func (r row) boolean(k string) bool { return r.i64(k) != 0 }

func (r row) nullInt(k string) sql.NullInt64 {
	if r.null(k) {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: r.i64(k), Valid: true}
}

func (r row) nullFloat(k string) sql.NullFloat64 {
	switch v := r[k].(type) {
	case nil:
		return sql.NullFloat64{}
	case float64:
		return sql.NullFloat64{Float64: v, Valid: true}
	case int64:
		return sql.NullFloat64{Float64: float64(v), Valid: true}
	case string:
		f, err := strconv.ParseFloat(v, 64)
		return sql.NullFloat64{Float64: f, Valid: err == nil}
	case []byte:
		f, err := strconv.ParseFloat(string(v), 64)
		return sql.NullFloat64{Float64: f, Valid: err == nil}
	}
	return sql.NullFloat64{}
}

// time reads a Django datetime (naive UTC text, or a driver-parsed time).
func (r row) time(k string) (time.Time, bool) {
	switch v := r[k].(type) {
	case nil:
		return time.Time{}, false
	case time.Time:
		return v.UTC(), true
	case string:
		return parseDjangoTime(v)
	case []byte:
		return parseDjangoTime(string(v))
	}
	return time.Time{}, false
}

// stamp is a Django datetime as an RFC 3339 UTC string, NULL when missing.
func (r row) stamp(k string) sql.NullString {
	t, ok := r.time(k)
	if !ok {
		return sql.NullString{}
	}
	return sql.NullString{String: stampOf(t), Valid: true}
}

var djangoLayouts = []string{
	"2006-01-02 15:04:05", // fractional seconds are accepted when parsing
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02T15:04:05",
	"2006-01-02",
}

func parseDjangoTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, l := range djangoLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func stampOf(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func nstr(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }
