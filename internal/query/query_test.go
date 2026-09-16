package query

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "people.parquet")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Errorf("close fixture: %v", err)
		}
	})
	if err := writePeopleFixture(f); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestComparisons(t *testing.T) {
	path := fixture(t)
	for _, tc := range []struct {
		op   string
		want []int64
	}{
		{"eq", []int64{2}}, {"ne", []int64{1, 3}}, {"gt", []int64{3}},
		{"ge", []int64{2, 3}}, {"lt", []int64{1}}, {"le", []int64{1, 2}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			var out bytes.Buffer
			err := Run(t.Context(), &out, WithFile(path), WithColumn("age"), WithOperator(tc.op), WithValue(37), WithBatchSize(2))
			if err != nil {
				t.Fatal(err)
			}
			var got []int64
			dec := json.NewDecoder(&out)
			for {
				var row struct {
					ID int64 `json:"id"`
				}
				if err := dec.Decode(&row); err == io.EOF {
					break
				} else if err != nil {
					t.Fatal(err)
				}
				got = append(got, row.ID)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestExplainAndValidation(t *testing.T) {
	opts := []Option{WithFile(fixture(t)), WithColumn("age"), WithOperator("ge"), WithValue(37), WithBatchSize(2), WithExplain(true)}
	var out bytes.Buffer
	if err := Run(t.Context(), &out, opts...); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out.Bytes()) || !strings.Contains(out.String(), "functions_comparison") || !strings.Contains(out.String(), "filter") {
		t.Fatalf("invalid plan: %s", &out)
	}
	for _, override := range []Option{
		WithColumn("missing"), WithColumn("name"), WithOperator("bad"),
		WithBatchSize(0), WithFile("missing.parquet"),
	} {
		bad := append(append([]Option(nil), opts...), override)
		if err := Run(t.Context(), io.Discard, bad...); err == nil {
			t.Fatal("accepted invalid options")
		}
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := Run(ctx, io.Discard, opts...); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestOutputAndNoMatches(t *testing.T) {
	opts := []Option{WithFile(fixture(t)), WithColumn("age"), WithOperator("ge"), WithValue(100), WithBatchSize(1)}
	var out bytes.Buffer
	if err := Run(t.Context(), &out, opts...); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no matches, got %s", &out)
	}
	opts = append(opts, WithValue(0))
	if err := Run(t.Context(), brokenWriter{}, opts...); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("got %v", err)
	}
}
