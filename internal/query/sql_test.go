package query

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/substrait-io/substrait-go/v8/plan"
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
			if err := Run(t.Context(), &out, WithFile(path), WithQuery(tc.sql), WithBatchSize(1)); err != nil {
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
				err := Run(t.Context(), &out, WithFile(path), WithQuery(sql), WithExplain(explain))
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

func TestSQLPlan(t *testing.T) {
	opts := defaultOptions()
	WithQuery("SELECT name, id FROM logs WHERE age >= 30 LIMIT 2")(&opts)
	p, err := buildPlan(peopleSchema(), opts)
	if err != nil {
		t.Fatal(err)
	}

	// Inspect the same plan compiled for execution; the predicate's age column
	// need not be selected, and Project emits just its appended expressions.
	root := p.GetRoots()[0]
	fetch := root.Input().(*plan.FetchRel)
	project := fetch.Input().(*plan.ProjectRel)
	filter := project.Input().(*plan.FilterRel)
	if _, ok := filter.Input().(*plan.NamedTableReadRel); !ok {
		t.Fatal("expected read relation")
	}
	if fetch.Count() != 2 || fetch.Offset() != 0 {
		t.Fatalf("unexpected fetch: %v", fetch)
	}
	if got := project.OutputMapping(); len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Fatalf("unexpected output mapping: %v", got)
	}

	execution, err := compilePlan(p, peopleSchema())
	if err != nil {
		t.Fatal(err)
	}
	if execution.limit != 2 || len(execution.columns) != 2 || execution.columns[0] != 1 || execution.columns[1] != 0 {
		t.Fatalf("unexpected execution: %+v", execution)
	}

	var out bytes.Buffer
	if err := writePlan(&out, p); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out.Bytes()) || !strings.Contains(out.String(), "fetch") || !strings.Contains(out.String(), "project") {
		t.Fatalf("invalid plan JSON: %s", &out)
	}
}

func TestSQLLimitSummary(t *testing.T) {
	path := fixture(t)
	for _, tc := range []struct {
		sql                       string
		scanned, written, batches int
	}{
		{"SELECT id FROM logs WHERE age > 30 LIMIT 1", 2, 1, 1},
		{"SELECT id FROM logs LIMIT 3", 3, 3, 2},
		{"SELECT id FROM logs LIMIT 0", 0, 0, 0},
		{"SELECT id FROM logs WHERE age > 100 LIMIT 1", 4, 0, 2},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			if err := Run(t.Context(), io.Discard, WithFile(path), WithQuery(tc.sql), WithBatchSize(2), WithLogger(logger)); err != nil {
				t.Fatal(err)
			}
			var summary struct {
				Scanned int `json:"rows_scanned"`
				Written int `json:"rows_written"`
				Batches int `json:"batches"`
			}
			if err := json.Unmarshal(logs.Bytes(), &summary); err != nil {
				t.Fatal(err)
			}
			if summary.Scanned != tc.scanned || summary.Written != tc.written || summary.Batches != tc.batches {
				t.Fatalf("incorrect summary: %+v", summary)
			}
		})
	}
}

func TestSQLModeAndErrors(t *testing.T) {
	path := fixture(t)
	for _, legacy := range []Option{WithColumn("age"), WithOperator("ge"), WithValue(0), WithPattern("")} {
		if err := Run(t.Context(), io.Discard, WithFile(path), WithQuery("SELECT * FROM logs"), legacy); err == nil {
			t.Fatal("accepted mixed query modes")
		}
	}

	for _, explain := range []bool{false, true} {
		err := Run(t.Context(), brokenWriter{}, WithFile(path), WithQuery("SELECT * FROM logs"), WithExplain(explain))
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("lost output error: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(t.Context())
	var out bytes.Buffer
	writer := cancelWriter{Writer: &out, cancel: cancel}
	err := Run(ctx, writer, WithFile(path), WithQuery("SELECT * FROM logs"))
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
