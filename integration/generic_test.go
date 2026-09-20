package integration_test

import (
	"bytes"
	"fmt"
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

func genericFixture(t *testing.T) string {
	t.Helper()
	schema := arrow.NewSchema([]arrow.Field{
		{Name: "product", Type: arrow.BinaryTypes.String, Nullable: true},
		{Name: "small", Type: arrow.PrimitiveTypes.Int8, Nullable: true},
		{Name: "medium", Type: arrow.PrimitiveTypes.Int16, Nullable: true},
		{Name: "quantity", Type: arrow.PrimitiveTypes.Int32, Nullable: true},
		{Name: "serial", Type: arrow.PrimitiveTypes.Int64, Nullable: true},
	}, nil)
	builder := array.NewRecordBuilder(memory.DefaultAllocator, schema)
	defer builder.Release()
	valid := []bool{true, true, false}
	builder.Field(0).(*array.StringBuilder).AppendValues([]string{"apple", "pear", ""}, valid)
	builder.Field(1).(*array.Int8Builder).AppendValues([]int8{math.MinInt8, math.MaxInt8, 0}, valid)
	builder.Field(2).(*array.Int16Builder).AppendValues([]int16{math.MinInt16, math.MaxInt16, 0}, valid)
	builder.Field(3).(*array.Int32Builder).AppendValues([]int32{math.MinInt32, math.MaxInt32, 0}, valid)
	builder.Field(4).(*array.Int64Builder).AppendValues([]int64{math.MinInt64, math.MaxInt64, 0}, valid)
	record := builder.NewRecordBatch()
	defer record.Release()
	path := filepath.Join(t.TempDir(), "inventory.parquet")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Errorf("close fixture: %v", err)
		}
	}()
	writer, err := pqarrow.NewFileWriter(schema, struct{ io.Writer }{f}, nil, pqarrow.DefaultWriterProps())
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Write(record); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGenericFilterAndSQLAgree(t *testing.T) {
	path := genericFixture(t)
	for _, tc := range []struct {
		column string
		value  int64
	}{
		{"small", math.MinInt8}, {"small", math.MaxInt8},
		{"medium", math.MinInt16}, {"medium", math.MaxInt16},
		{"quantity", math.MinInt32}, {"quantity", math.MaxInt32},
		{"serial", math.MinInt64}, {"serial", math.MaxInt64},
	} {
		t.Run(fmt.Sprintf("%s/%d", tc.column, tc.value), func(t *testing.T) {
			var filter, sql bytes.Buffer
			if _, err := query.Run(t.Context(), &filter, query.WithFile(path), query.WithColumn(tc.column), query.WithOperator("eq"), query.WithValue(tc.value), query.WithBatchSize(1)); err != nil {
				t.Fatal(err)
			}
			text := fmt.Sprintf("SELECT * FROM logs WHERE %s = %d", tc.column, tc.value)
			if _, err := query.Run(t.Context(), &sql, query.WithFile(path), query.WithPlanBuilder(sqlquery.Planner(text)), query.WithBatchSize(1)); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(filter.Bytes(), sql.Bytes()) || bytes.Count(filter.Bytes(), []byte("\n")) != 1 {
				t.Fatalf("filter %s; SQL %s", &filter, &sql)
			}
		})
	}
	for _, tc := range []struct {
		value string
		count int
	}{{"pear", 1}, {"", 0}} {
		var filter, sql bytes.Buffer
		if _, err := query.Run(t.Context(), &filter, query.WithFile(path), query.WithColumn("product"), query.WithOperator("eq"), query.WithStringValue(tc.value)); err != nil {
			t.Fatal(err)
		}
		if _, err := query.Run(t.Context(), &sql, query.WithFile(path), query.WithPlanBuilder(sqlquery.Planner(fmt.Sprintf("SELECT * FROM logs WHERE product = '%s'", tc.value)))); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(filter.Bytes(), sql.Bytes()) || bytes.Count(filter.Bytes(), []byte("\n")) != tc.count {
			t.Fatalf("filter %s; SQL %s", &filter, &sql)
		}
	}
}

func TestGenericFiltersRejectWrongTypesAndOverflow(t *testing.T) {
	path := genericFixture(t)
	for _, tc := range []struct {
		column string
		value  int64
	}{
		{"small", math.MinInt8 - 1}, {"small", math.MaxInt8 + 1},
		{"medium", math.MinInt16 - 1}, {"medium", math.MaxInt16 + 1},
		{"quantity", math.MinInt32 - 1}, {"quantity", math.MaxInt32 + 1},
		{"product", 1},
	} {
		for _, opts := range [][]query.Option{
			{query.WithColumn(tc.column), query.WithValue(tc.value)},
			{query.WithPlanBuilder(sqlquery.Planner(fmt.Sprintf("SELECT * FROM logs WHERE %s = %d", tc.column, tc.value)))},
		} {
			var out bytes.Buffer
			opts = append(opts, query.WithFile(path))
			if _, err := query.Run(t.Context(), &out, opts...); err == nil {
				t.Errorf("accepted %s = %d", tc.column, tc.value)
			}
			if out.Len() != 0 {
				t.Errorf("invalid query produced %s", &out)
			}
		}
	}
	if _, err := query.Run(t.Context(), io.Discard, query.WithFile(path), query.WithColumn("small"), query.WithStringValue("1")); err == nil {
		t.Error("accepted string for integer column")
	}
}
