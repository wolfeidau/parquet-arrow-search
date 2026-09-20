package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/wolfeidau/parquet-arrow-search/internal/query"
	sqlquery "github.com/wolfeidau/parquet-arrow-search/internal/sql"
)

func TestSQLQueries(t *testing.T) {
	path := fixture(t)
	for _, tc := range []struct {
		name, sql, want string
	}{
		{"projection and filter", "SELECT name FROM logs WHERE age >= 37", "{\"name\":\"Grace\"}\n{\"name\":\"Linus\"}\n"},
		{"limit", "SELECT id FROM logs LIMIT 2", "{\"id\":1}\n{\"id\":2}\n"},
		{"zero limit", "SELECT * FROM logs LIMIT 0", ""},
		{"filter before limit", "SELECT id FROM logs WHERE age > 30 LIMIT 1", "{\"id\":2}\n"},
		{"null", "SELECT name FROM logs WHERE age IS NULL", "{\"name\":\"Unknown\"}\n"},
		{"not null", "SELECT id FROM logs WHERE age IS NOT NULL LIMIT 1", "{\"id\":1}\n"},
		{"string equality", "SELECT id FROM logs WHERE name = 'Grace'", "{\"id\":2}\n"},
		{"string order", "SELECT id FROM logs WHERE name < 'Grace'", "{\"id\":1}\n"},
		{"regex", "SELECT id FROM logs WHERE regex(name, '(?i)^gr')", "{\"id\":2}\n"},
		{"regex backslashes", `SELECT id FROM logs WHERE regex(name, '\bAda\b')`, "{\"id\":1}\n"},
		{"empty regex", "SELECT id FROM logs WHERE regex(name, '') LIMIT 1", "{\"id\":1}\n"},
		{"quote escaping", "SELECT id FROM logs WHERE name = 'O''Brien'", ""},
		{"case and terminator", "select \"id\" from logs where age = 28 limit 1;", "{\"id\":1}\n"},
		{"no matches", "SELECT * FROM logs WHERE age > 100", ""},
		{"null excluded from inequality", "SELECT id FROM logs WHERE age <> 37", "{\"id\":1}\n{\"id\":3}\n"},
		{"int64 minimum", "SELECT id FROM logs WHERE age < -9223372036854775808", ""},
		{"int64 maximum", "SELECT id FROM logs WHERE age > 9223372036854775807", ""},
		{"max limit", "SELECT id FROM logs WHERE id = 1 LIMIT 9223372036854775807", "{\"id\":1}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if _, err := query.Run(t.Context(), &out, query.WithFile(path), query.WithPlanBuilder(sqlquery.Planner(tc.sql)), query.WithBatchSize(1)); err != nil {
				t.Fatal(err)
			}
			if out.String() != tc.want {
				t.Fatalf("got %q, want %q", out.String(), tc.want)
			}
		})
	}
}

func TestSQLRejectsInvalidQueriesBeforeOutput(t *testing.T) {
	path := fixture(t)
	for _, sql := range []string{
		"", "SELECT FROM logs", "SELECT * FROM other", "SELECT * FROM LOGS",
		"SELECT * FROM logs WHERE age > 1 AND id = 2",
		"SELECT * FROM logs WHERE age > 1 OR id = 2",
		"SELECT * FROM logs ORDER BY id", "SELECT count(*) FROM logs",
		"SELECT * FROM logs JOIN other", "SELECT * FROM logs; SELECT * FROM logs",
		"SELECT id, id FROM logs", "SELECT missing FROM logs",
		"SELECT * FROM logs WHERE missing = 1", "SELECT * FROM logs WHERE Age = 1",
		"SELECT * FROM logs WHERE age = '37'", "SELECT * FROM logs WHERE name = 37",
		"SELECT * FROM logs WHERE age = NULL", "SELECT * FROM logs WHERE age = 3.7",
		"SELECT * FROM logs WHERE age = 9223372036854775808",
		"SELECT * FROM logs WHERE age = -9223372036854775809",
		"SELECT * FROM logs LIMIT -1", "SELECT * FROM logs LIMIT 9223372036854775808",
		"SELECT * FROM logs WHERE regex(age, 'a')",
		"SELECT * FROM logs WHERE regex(name, '[')",
		"SELECT * FROM logs WHERE regex(name, '(?=a)') LIMIT 0",
		"SELECT * FROM logs WHERE regex(name, '(a)\\1')",
		"SELECT * FROM logs WHERE name = 'unclosed",
	} {
		t.Run(sql, func(t *testing.T) {
			for _, explain := range []bool{false, true} {
				var out bytes.Buffer
				_, err := query.Run(t.Context(), &out, query.WithFile(path), query.WithPlanBuilder(sqlquery.Planner(sql)), query.WithExplain(explain))
				if err == nil {
					t.Fatal("expected error")
				}
				if out.Len() != 0 {
					t.Fatalf("invalid query produced output: %s", &out)
				}
			}
		})
	}
}

func TestSQLExplain(t *testing.T) {
	var out bytes.Buffer
	result, err := query.Run(t.Context(), &out, query.WithFile(fixture(t)), query.WithPlanBuilder(sqlquery.Planner("SELECT name, id FROM logs WHERE age >= 30 LIMIT 2")), query.WithExplain(true))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Explain || result.Scanned != 0 || result.Written != 0 {
		t.Fatalf("unexpected explain result: %+v", result)
	}
	if !json.Valid(out.Bytes()) || !strings.Contains(out.String(), "fetch") || !strings.Contains(out.String(), "project") {
		t.Fatalf("invalid plan JSON: %s", &out)
	}
}

func TestSQLLimitResult(t *testing.T) {
	path := fixture(t)
	for _, tc := range []struct {
		sql                       string
		scanned, written, batches int64
	}{
		{"SELECT id FROM logs WHERE age > 30 LIMIT 1", 2, 1, 1},
		{"SELECT id FROM logs LIMIT 3", 3, 3, 2},
		{"SELECT id FROM logs LIMIT 0", 0, 0, 0},
		{"SELECT id FROM logs WHERE age > 100 LIMIT 1", 4, 0, 2},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			result, err := query.Run(t.Context(), io.Discard, query.WithFile(path), query.WithPlanBuilder(sqlquery.Planner(tc.sql)), query.WithBatchSize(2))
			if err != nil {
				t.Fatal(err)
			}
			if result.Scanned != tc.scanned || result.Written != tc.written || result.Matched != tc.written || result.Batches != tc.batches || result.Explain || result.Elapsed <= 0 {
				t.Fatalf("incorrect result: %+v", result)
			}
		})
	}
}

func TestSQLOutputErrorsAndCancellation(t *testing.T) {
	path := fixture(t)

	for _, explain := range []bool{false, true} {
		_, err := query.Run(t.Context(), brokenWriter{}, query.WithFile(path), query.WithPlanBuilder(sqlquery.Planner("SELECT * FROM logs")), query.WithExplain(explain))
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("lost output error: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(t.Context())
	var out bytes.Buffer
	writer := cancelWriter{Writer: &out, cancel: cancel}
	_, err := query.Run(ctx, writer, query.WithFile(path), query.WithPlanBuilder(sqlquery.Planner("SELECT * FROM logs")))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
	if bytes.Count(out.Bytes(), []byte("\n")) != 1 {
		t.Fatal("continued writing after cancellation")
	}
}

type cancelWriter struct {
	io.Writer
	cancel context.CancelFunc
}

func (w cancelWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	w.cancel()
	return n, err
}
