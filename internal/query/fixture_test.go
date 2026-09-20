package query

import (
	"fmt"
	"io"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/arrow-go/v18/parquet/pqarrow"
	"github.com/substrait-io/substrait-go/v8/expr"
	"github.com/substrait-io/substrait-go/v8/extensions"
	"github.com/substrait-io/substrait-go/v8/plan"
	"github.com/wolfeidau/parquet-arrow-search/internal/schema"
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
		return err
	}
	if err := w.Write(record); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}

func peopleSchema() *arrow.Schema {
	return arrow.NewSchema([]arrow.Field{
		{Name: "id", Type: arrow.PrimitiveTypes.Int64},
		{Name: "name", Type: arrow.BinaryTypes.String},
		{Name: "age", Type: arrow.PrimitiveTypes.Int64, Nullable: true},
	}, nil)
}

// agePlanner builds a known Substrait shape to test execution independently of front ends.
func agePlanner(operator string, value int64) func(*arrow.Schema) (*plan.Plan, error) {
	return func(fileSchema *arrow.Schema) (*plan.Plan, error) {
		ns, err := schema.SubstraitSchema(fileSchema)
		if err != nil {
			return nil, fmt.Errorf("convert test schema: %w", err)
		}

		b := plan.NewBuilderDefault()
		scan := b.NamedScan([]string{"people"}, ns)
		ref, err := b.RootFieldRef(scan, 2)
		if err != nil {
			return nil, fmt.Errorf("reference test age: %w", err)
		}

		literal, err := expr.NewLiteral(value, false)
		if err != nil {
			return nil, fmt.Errorf("build test literal: %w", err)
		}
		condition, err := b.ScalarFn(extensions.SubstraitDefaultURNPrefix+"functions_comparison", operator, nil, ref, literal)
		if err != nil {
			return nil, fmt.Errorf("build test comparison: %w", err)
		}

		filtered, err := b.Filter(scan, condition)
		if err != nil {
			return nil, fmt.Errorf("build test filter: %w", err)
		}
		p, err := b.Plan(filtered, ns.Names)
		if err != nil {
			return nil, fmt.Errorf("build test plan: %w", err)
		}
		return p, nil
	}
}
