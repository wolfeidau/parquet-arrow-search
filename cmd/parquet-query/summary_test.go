package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/wolfeidau/parquet-arrow-search/internal/query"
)

func TestLogSummary(t *testing.T) {
	for _, tc := range []struct {
		name    string
		result  query.Result
		failed  bool
		status  string
		message string
		rate    float64
	}{
		{name: "success", result: query.Result{Elapsed: 2 * time.Second, Batches: 2, Scanned: 10, Matched: 3, Written: 3}, status: "ok", message: "query finished", rate: 5},
		{name: "failure", result: query.Result{Elapsed: time.Second, Batches: 1, Scanned: 4, Matched: 2, Written: 1}, failed: true, status: "failed", message: "query finished", rate: 4},
		{name: "explain", result: query.Result{Elapsed: time.Second, Explain: true}, status: "ok", message: "plan finished"},
		{name: "failed explain", result: query.Result{Explain: true}, failed: true, status: "failed", message: "plan finished"},
		{name: "zero elapsed", result: query.Result{Scanned: 10}, status: "ok", message: "query finished"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			logSummary(t.Context(), slog.New(slog.NewJSONHandler(&out, nil)), tc.result, tc.failed)
			var entry map[string]any
			if err := json.Unmarshal(out.Bytes(), &entry); err != nil {
				t.Fatal(err)
			}
			if entry["msg"] != tc.message || entry["status"] != tc.status || entry["elapsed"] != float64(tc.result.Elapsed) {
				t.Fatalf("unexpected summary: %s", out.String())
			}
			for key, want := range map[string]float64{
				"batches": float64(tc.result.Batches), "rows_scanned": float64(tc.result.Scanned),
				"rows_matched": float64(tc.result.Matched), "rows_written": float64(tc.result.Written), "rows_per_second": tc.rate,
			} {
				got, exists := entry[key]
				if tc.result.Explain {
					if exists {
						t.Errorf("explain summary includes %s", key)
					}
				} else if !exists || got != want {
					t.Errorf("%s = %v, want %v", key, got, want)
				}
			}
		})
	}
}

func TestExecuteOutputSeparation(t *testing.T) {
	for _, explain := range []bool{false, true} {
		c, err := parseCLI(t, "query", "--file=../../testdata/bash-example.parquet", "--query=SELECT content FROM logs LIMIT 1")
		if err != nil {
			t.Fatal(err)
		}
		c.Explain = explain
		var out, logs bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		if err := execute(t.Context(), &out, logger, c.options(logger)...); err != nil {
			t.Fatal(err)
		}
		if !json.Valid(out.Bytes()) {
			t.Fatalf("stdout is not JSON: %s", out.String())
		}
		message := "query finished"
		if explain {
			message = "plan finished"
		}
		if strings.Contains(out.String(), message) || strings.Count(logs.String(), message) != 1 {
			t.Fatalf("summary separation failed: stdout=%s stderr=%s", out.String(), logs.String())
		}
		if !strings.Contains(logs.String(), "starting query") {
			t.Fatal("missing debug logs")
		}
	}
}

type failingOutput struct{}

func (failingOutput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestExecuteFailureSummary(t *testing.T) {
	c, err := parseCLI(t, "query", "--file=../../testdata/bash-example.parquet", "--query=SELECT content FROM logs LIMIT 1")
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	err = execute(t.Context(), failingOutput{}, logger, c.options(logger)...)
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("error = %v", err)
	}
	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["status"] != "failed" || entry["rows_matched"] != float64(1) || entry["rows_written"] != float64(0) {
		t.Fatalf("unexpected partial summary: %s", logs.String())
	}
	if strings.Contains(logs.String(), "query failed") {
		t.Fatal("error should be logged only by main")
	}
}
