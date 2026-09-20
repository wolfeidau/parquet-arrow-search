package query

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/substrait-io/substrait-go/v8/plan"
)

func TestRunDefaults(t *testing.T) {
	var output, logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	result, err := Run(t.Context(), &output, WithFile(fixture(t)), WithPlanBuilder(agePlanner("gte", 0)), WithLogger(logger))
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(output.Bytes(), []byte("\n")); got != 3 {
		t.Fatalf("got %d rows, want 3", got)
	}
	if result.Batches != 1 || result.Scanned != 4 || result.Matched != 3 || result.Written != 3 || result.Explain {
		t.Fatalf("incorrect default result: %+v", result)
	}
	if logs.Len() != 0 {
		t.Fatalf("engine logged summary: %s", &logs)
	}
}

func TestOptionOverrides(t *testing.T) {
	opts := []Option{WithFile(fixture(t)), WithPlanBuilder(agePlanner("gte", 100)), WithExplain(true)}
	var output bytes.Buffer
	opts = append(opts, WithPlanBuilder(agePlanner("equal", 37)), WithExplain(false), WithLogger(nil))
	if _, err := Run(t.Context(), &output, opts...); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "{\"age\":37,\"id\":2,\"name\":\"Grace\"}\n" {
		t.Fatalf("unexpected output: %s", got)
	}
	for _, size := range []int64{0, -1} {
		if _, err := Run(t.Context(), io.Discard, WithFile(fixture(t)), WithPlanBuilder(agePlanner("gte", 0)), WithBatchSize(size)); err == nil {
			t.Errorf("accepted batch size %d", size)
		}
	}
}

func TestRequiredOptions(t *testing.T) {
	for _, opts := range [][]Option{nil, {WithFile(fixture(t))}, {WithPlanBuilder(agePlanner("gte", 0))}, {WithFile(fixture(t)), WithPlanBuilder(agePlanner("gte", 0)), WithPlanBuilder(nil)}} {
		if _, err := Run(t.Context(), io.Discard, opts...); err == nil {
			t.Fatal("accepted missing file or plan builder")
		}
	}
}

func TestPlanBuilderErrors(t *testing.T) {
	failure := errors.New("planner failed")
	for _, tc := range []struct {
		name    string
		failure error
	}{
		{"propagated error", failure}, {"nil plan", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			_, err := Run(t.Context(), &out,
				WithFile(fixture(t)),
				WithPlanBuilder(func(*arrow.Schema) (*plan.Plan, error) { return nil, tc.failure }),
			)
			if err == nil {
				t.Fatal("expected error")
			}
			if tc.failure != nil && !errors.Is(err, tc.failure) {
				t.Fatalf("lost planner error: %v", err)
			}
			if out.Len() != 0 {
				t.Fatalf("failed planner produced %s", &out)
			}
		})
	}
}
