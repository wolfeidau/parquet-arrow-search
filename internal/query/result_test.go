package query

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
)

func TestRunResult(t *testing.T) {
	path := fixture(t)

	for _, tc := range []struct {
		name                               string
		explain, fail                      bool
		batches, scanned, matched, written int64
	}{
		{name: "success", batches: 2, scanned: 4, matched: 2, written: 2},
		{name: "output failure", fail: true, batches: 1, scanned: 2, matched: 1},
		{name: "explain", explain: true},
		{name: "explain output failure", explain: true, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output, logs bytes.Buffer
			var out io.Writer = &output
			if tc.fail {
				out = brokenWriter{}
			}

			logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			result, err := Run(t.Context(), out,
				WithFile(path),
				WithColumn("age"),
				WithValue(30),
				WithBatchSize(2),
				WithExplain(tc.explain),
				WithLogger(logger),
			)
			if (err != nil) != tc.fail {
				t.Fatalf("error: %v", err)
			}
			if tc.fail && !errors.Is(err, io.ErrClosedPipe) {
				t.Fatalf("lost output error: %v", err)
			}
			if result.Batches != tc.batches || result.Scanned != tc.scanned || result.Matched != tc.matched || result.Written != tc.written || result.Explain != tc.explain || result.Elapsed <= 0 {
				t.Fatalf("result: %+v", result)
			}
			if tc.explain && !tc.fail && !json.Valid(output.Bytes()) {
				t.Fatalf("invalid plan: %s", &output)
			}

			decoder := json.NewDecoder(&logs)
			for decoder.More() {
				var entry struct{ Level string }
				if err := decoder.Decode(&entry); err != nil {
					t.Fatal(err)
				}
				if entry.Level != "DEBUG" {
					t.Fatalf("engine logged non-debug entry: %s", entry.Level)
				}
			}
		})
	}
}

func TestRunResultBeforeScanning(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	for _, tc := range []struct {
		name string
		ctx  context.Context
		opts []Option
	}{
		{"missing file", t.Context(), []Option{WithFile("missing.parquet"), WithColumn("age")}},
		{"invalid options", t.Context(), nil},
		{"cancelled", ctx, []Option{WithFile(fixture(t)), WithColumn("age")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Run(tc.ctx, io.Discard, append(tc.opts, WithExplain(true))...)
			if err == nil {
				t.Fatal("expected failure")
			}
			if !result.Explain || result.Elapsed <= 0 || result.Batches != 0 || result.Scanned != 0 || result.Matched != 0 || result.Written != 0 {
				t.Fatalf("result: %+v", result)
			}
		})
	}
}

type cancellingOutput struct {
	cancel context.CancelFunc
}

func (w cancellingOutput) Write(data []byte) (int, error) {
	w.cancel()
	return len(data), nil
}

func TestRunResultAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	result, err := Run(ctx, cancellingOutput{cancel: cancel},
		WithFile(fixture(t)),
		WithColumn("age"),
		WithBatchSize(2),
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
	if result.Batches != 1 || result.Scanned != 1 || result.Matched != 1 || result.Written != 1 || result.Elapsed <= 0 || result.Explain {
		t.Fatalf("partial result: %+v", result)
	}
}
