package query

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/arrow-go/v18/parquet/pqarrow"
)

func TestRegex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs.parquet")
	schema := arrow.NewSchema([]arrow.Field{
		{Name: "Timestamp", Type: arrow.PrimitiveTypes.Int64},
		{Name: "Content", Type: arrow.BinaryTypes.String, Nullable: true},
		{Name: "Group", Type: arrow.BinaryTypes.String},
		{Name: "Flags", Type: arrow.PrimitiveTypes.Int32},
	}, nil)
	b := array.NewRecordBuilder(memory.DefaultAllocator, schema)
	defer b.Release()
	b.Field(0).(*array.Int64Builder).AppendValues([]int64{1, 2, 3, 4, 5, 6}, nil)
	b.Field(1).(*array.StringBuilder).AppendValues([]string{"ERROR: build failed", "ok", "prefix\npanic: boom", "", "", "日本語 error"}, []bool{true, true, true, true, false, true})
	b.Field(2).(*array.StringBuilder).AppendValues([]string{"build", "build", "test", "test", "test", "test"}, nil)
	b.Field(3).(*array.Int32Builder).AppendValues([]int32{0, 0, 1, 0, 0, 0}, nil)
	record := b.NewRecordBatch()
	defer record.Release()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Errorf("close fixture: %v", err)
		}
	})
	w, err := pqarrow.NewFileWriter(schema, struct{ io.Writer }{f}, nil, pqarrow.DefaultWriterProps())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Write(record); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		pattern string
		want    []int64
	}{
		{"(?i)error|failed|panic", []int64{1, 3, 6}},
		{"error", []int64{6}},
		{"^panic", nil},
		{"(?m)^panic", []int64{3}},
		{"(?s)prefix.*boom", []int64{3}},
		{"^$", []int64{4}},
		{"", []int64{1, 2, 3, 4, 6}},
		{"日本語", []int64{6}},
	} {
		t.Run(tc.pattern, func(t *testing.T) {
			var out bytes.Buffer
			opts := Options{File: path, Column: "Content", Op: "regex", Pattern: tc.pattern, BatchSize: 2}
			if err := Run(t.Context(), opts, &out); err != nil {
				t.Fatal(err)
			}
			var got []int64
			dec := json.NewDecoder(&out)
			for {
				var row struct{ Timestamp int64 }
				if err := dec.Decode(&row); err == io.EOF {
					break
				} else if err != nil {
					t.Fatal(err)
				}
				got = append(got, row.Timestamp)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
	opts := Options{File: path, Column: "Content", Op: "regex", Pattern: "error", BatchSize: 2, Explain: true}
	var out bytes.Buffer
	if err := Run(t.Context(), opts, &out); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out.Bytes()) || !strings.Contains(out.String(), regexURN) || !strings.Contains(out.String(), "go_regexp_match") {
		t.Fatalf("invalid regex plan: %s", &out)
	}
	for _, pattern := range []string{"[", "(?=error)", `(error)\1`} {
		for _, explain := range []bool{false, true} {
			opts.Pattern, opts.Explain = pattern, explain
			out.Reset()
			if err := Run(t.Context(), opts, &out); err == nil {
				t.Fatalf("accepted %q", pattern)
			}
			if out.Len() != 0 {
				t.Fatal("invalid pattern produced output")
			}
		}
	}
	opts.Pattern, opts.Column = "error", "Timestamp"
	if err := Run(t.Context(), opts, io.Discard); err == nil {
		t.Fatal("accepted regex on integer column")
	}
}
