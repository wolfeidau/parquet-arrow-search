package integration_test

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/arrow-go/v18/parquet/pqarrow"
)

// writePeopleFixture creates controlled values for comparison and null tests.
func writePeopleFixture(out io.Writer) error {
	schema := peopleSchema()
	b := array.NewRecordBuilder(memory.DefaultAllocator, schema)
	defer b.Release()
	b.Field(0).(*array.Int64Builder).AppendValues([]int64{1, 2, 3, 4}, nil)
	b.Field(1).(*array.StringBuilder).AppendValues([]string{"Ada", "Grace", "Linus", "Unknown"}, nil)
	b.Field(2).(*array.Int64Builder).AppendValues([]int64{28, 37, 45, 0}, []bool{true, true, true, false})
	record := b.NewRecordBatch()
	defer record.Release()
	w, err := pqarrow.NewFileWriter(schema, struct{ io.Writer }{out}, nil, pqarrow.DefaultWriterProps())
	if err != nil {
		return fmt.Errorf("create fixture writer: %w", err)
	}
	if err := w.Write(record); err != nil {
		writeErr := fmt.Errorf("write fixture: %w", err)
		if closeErr := w.Close(); closeErr != nil {
			return errors.Join(writeErr, fmt.Errorf("close fixture writer: %w", closeErr))
		}
		return writeErr
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close fixture writer: %w", err)
	}
	return nil
}

func peopleSchema() *arrow.Schema {
	return arrow.NewSchema([]arrow.Field{
		{Name: "id", Type: arrow.PrimitiveTypes.Int64},
		{Name: "name", Type: arrow.BinaryTypes.String},
		{Name: "age", Type: arrow.PrimitiveTypes.Int64, Nullable: true},
	}, nil)
}

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

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
