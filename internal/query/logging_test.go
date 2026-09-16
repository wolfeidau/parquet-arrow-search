package query

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

func TestQuerySummary(t *testing.T) {
	path := fixture(t)
	for _, tc := range []struct {
		name                      string
		explain, fail             bool
		scanned, matched, written float64
	}{
		{name: "success", scanned: 4, matched: 2, written: 2},
		{name: "output failure", fail: true, scanned: 2, matched: 1},
		{name: "explain", explain: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs, output bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			var out io.Writer = &output
			if tc.fail {
				out = brokenWriter{}
			}
			err := Run(t.Context(), Options{File: path, Column: "age", Op: "ge", Value: 30, BatchSize: 2, Explain: tc.explain, Logger: logger}, out)
			if (err != nil) != tc.fail {
				t.Fatalf("unexpected error: %v", err)
			}
			var summary map[string]any
			dec := json.NewDecoder(&logs)
			for dec.More() {
				var entry map[string]any
				if err := dec.Decode(&entry); err != nil {
					t.Fatal(err)
				}
				if entry["level"] == "INFO" {
					summary = entry
				}
			}
			if summary == nil {
				t.Fatal("missing summary")
			}
			status := "ok"
			if tc.fail {
				status = "failed"
			}
			if summary["status"] != status {
				t.Fatalf("summary: %v", summary)
			}
			if tc.explain {
				if summary["msg"] != "plan finished" || summary["rows_scanned"] != nil || !json.Valid(output.Bytes()) {
					t.Fatalf("invalid explain summary/output: %v", summary)
				}
				return
			}
			if summary["rows_scanned"] != tc.scanned || summary["rows_matched"] != tc.matched || summary["rows_written"] != tc.written {
				t.Fatalf("incorrect counts: %v", summary)
			}
			if summary["elapsed"].(float64) <= 0 || summary["rows_per_second"].(float64) <= 0 {
				t.Fatalf("invalid timing: %v", summary)
			}
		})
	}
}

func TestLogSamples(t *testing.T) {
	files, err := filepath.Glob("../../testdata/*.parquet")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("missing testdata")
	}
	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			var output bytes.Buffer
			if err := Run(t.Context(), Options{File: path, Column: "content", Op: regexOp, Pattern: "", BatchSize: 128}, &output); err != nil {
				t.Fatal(err)
			}
			dec := json.NewDecoder(&output)
			rows := 0
			for dec.More() {
				var row map[string]any
				if err := dec.Decode(&row); err != nil {
					t.Fatal(err)
				}
				for _, name := range strings.Fields("timestamp content group flags") {
					if _, ok := row[name]; !ok {
						t.Fatalf("missing column %q", name)
					}
				}
				rows++
			}
			if rows == 0 {
				t.Fatal("expected log rows")
			}
		})
	}
}
