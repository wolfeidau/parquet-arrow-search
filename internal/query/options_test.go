package query

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
)

func TestRunDefaults(t *testing.T) {
	var output, logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	if err := Run(t.Context(), &output, WithFile(fixture(t)), WithColumn("age"), WithLogger(logger)); err != nil {
		t.Fatal(err)
	}
	// The default ge 0 predicate includes all three non-null ages in one batch.
	if got := bytes.Count(output.Bytes(), []byte("\n")); got != 3 {
		t.Fatalf("got %d rows, want 3", got)
	}
	var summary struct {
		Batches int64 `json:"batches"`
	}
	if err := json.Unmarshal(logs.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Batches != 1 {
		t.Fatalf("got %d batches, want 1", summary.Batches)
	}
}

func TestOptionOverrides(t *testing.T) {
	opts := []Option{WithFile(fixture(t)), WithColumn("age"), WithValue(100), WithExplain(true)}
	var output bytes.Buffer
	opts = append(opts, WithValue(37), WithExplain(false), WithOperator("eq"), WithLogger(nil))
	if err := Run(t.Context(), &output, opts...); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "{\"age\":37,\"id\":2,\"name\":\"Grace\"}\n" {
		t.Fatalf("unexpected output: %s", got)
	}
	for _, size := range []int64{0, -1} {
		if err := Run(t.Context(), io.Discard, WithFile(fixture(t)), WithColumn("age"), WithBatchSize(size)); err == nil {
			t.Errorf("accepted batch size %d", size)
		}
	}
}

func TestRequiredOptions(t *testing.T) {
	for _, opts := range [][]Option{nil, {WithFile(fixture(t))}, {WithColumn("age")}} {
		if err := Run(t.Context(), io.Discard, opts...); err == nil {
			t.Fatal("accepted missing file or column")
		}
	}
}
