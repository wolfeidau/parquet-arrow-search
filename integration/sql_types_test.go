package integration_test

import (
	"bytes"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/arrow-go/v18/parquet/pqarrow"
	"github.com/wolfeidau/parquet-arrow-search/internal/query"
	sqlquery "github.com/wolfeidau/parquet-arrow-search/internal/sql"
)

func sqlTypesFixture(t *testing.T) string {
	t.Helper()
	schema := arrow.NewSchema([]arrow.Field{
		{Name: "flags", Type: arrow.PrimitiveTypes.Int32, Nullable: true},
		{Name: "content", Type: arrow.BinaryTypes.String, Nullable: true},
		{Name: "select", Type: arrow.FixedWidthTypes.Boolean},
		{Name: `odd"name`, Type: arrow.PrimitiveTypes.Float64},
	}, nil)
	b := array.NewRecordBuilder(memory.DefaultAllocator, schema)
	defer b.Release()
	b.Field(0).(*array.Int32Builder).AppendValues([]int32{math.MinInt32, 0, math.MaxInt32, 0}, []bool{true, true, true, false})
	b.Field(1).(*array.StringBuilder).AppendValues([]string{"O'Brien", "日本語 error", "", ""}, []bool{true, true, true, false})
	b.Field(2).(*array.BooleanBuilder).AppendValues([]bool{true, false, true, false}, nil)
	b.Field(3).(*array.Float64Builder).AppendValues([]float64{1, 2, 3, 4}, nil)
	record := b.NewRecordBatch()
	defer record.Release()

	path := filepath.Join(t.TempDir(), "types.parquet")
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
	return path
}

func TestSQLTypesAndQuoting(t *testing.T) {
	path := sqlTypesFixture(t)
	for _, tc := range []struct{ sql, want string }{
		{`SELECT flags FROM logs WHERE flags = -2147483648`, "{\"flags\":-2147483648}\n"},
		{`SELECT flags FROM logs WHERE flags >= 2147483647`, "{\"flags\":2147483647}\n"},
		{`SELECT flags FROM logs WHERE flags <= 0`, "{\"flags\":-2147483648}\n{\"flags\":0}\n"},
		{`SELECT flags FROM logs WHERE flags != 0`, "{\"flags\":-2147483648}\n{\"flags\":2147483647}\n"},
		{`SELECT flags FROM logs WHERE flags > 0`, "{\"flags\":2147483647}\n"},
		{`SELECT flags FROM logs WHERE flags < 0`, "{\"flags\":-2147483648}\n"},
		{`SELECT flags FROM logs WHERE flags IS NULL`, "{\"flags\":null}\n"},
		{`SELECT flags FROM logs WHERE content = 'O''Brien'`, "{\"flags\":-2147483648}\n"},
		{`SELECT flags FROM logs WHERE regex(content, '日本語')`, "{\"flags\":0}\n"},
		{`SELECT flags FROM logs WHERE regex(content, '^$')`, "{\"flags\":2147483647}\n"},
		{`SELECT flags FROM logs WHERE content IS NULL`, "{\"flags\":null}\n"},
		{`SELECT "select", "odd""name" FROM logs LIMIT 1`, "{\"odd\\\"name\":1,\"select\":true}\n"},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			var out bytes.Buffer
			if _, err := query.Run(t.Context(), &out, query.WithFile(path), query.WithPlanBuilder(sqlquery.Planner(tc.sql)), query.WithBatchSize(2)); err != nil {
				t.Fatal(err)
			}
			if out.String() != tc.want {
				t.Fatalf("got %q, want %q", out.String(), tc.want)
			}
		})
	}

	for _, sql := range []string{
		`SELECT * FROM logs WHERE flags = 2147483648`,
		`SELECT * FROM logs WHERE flags = -2147483649`,
		`SELECT * FROM logs WHERE "select" = 1`,
		`SELECT * FROM logs WHERE "odd""name" = 1`,
	} {
		if _, err := query.Run(t.Context(), io.Discard, query.WithFile(path), query.WithPlanBuilder(sqlquery.Planner(sql))); err == nil {
			t.Errorf("accepted %s", sql)
		}
	}
}
