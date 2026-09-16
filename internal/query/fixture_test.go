package query

import (
	"io"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/arrow-go/v18/parquet/pqarrow"
)

// writePeopleFixture creates controlled values for comparison and null tests.
func writePeopleFixture(out io.Writer) error {
	schema := arrow.NewSchema([]arrow.Field{
		{Name: "id", Type: arrow.PrimitiveTypes.Int64},
		{Name: "name", Type: arrow.BinaryTypes.String},
		{Name: "age", Type: arrow.PrimitiveTypes.Int64, Nullable: true},
	}, nil)
	b := array.NewRecordBuilder(memory.DefaultAllocator, schema)
	defer b.Release()
	b.Field(0).(*array.Int64Builder).AppendValues([]int64{1, 2, 3, 4}, nil)
	b.Field(1).(*array.StringBuilder).AppendValues([]string{"Ada", "Grace", "Linus", "Unknown"}, nil)
	b.Field(2).(*array.Int64Builder).AppendValues([]int64{28, 37, 45, 0}, []bool{true, true, true, false})
	record := b.NewRecordBatch()
	defer record.Release()
	w, err := pqarrow.NewFileWriter(schema, struct{ io.Writer }{out}, nil, pqarrow.DefaultWriterProps())
	if err != nil {
		return err
	}
	if err := w.Write(record); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}
