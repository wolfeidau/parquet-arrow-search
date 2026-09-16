// Package query implements a small Substrait executor for local Parquet files.
package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"regexp"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/arrow-go/v18/parquet/file"
	"github.com/apache/arrow-go/v18/parquet/pqarrow"
	"github.com/substrait-io/substrait-go/v8/expr"
	"github.com/substrait-io/substrait-go/v8/extensions"
	"github.com/substrait-io/substrait-go/v8/plan"
	"github.com/substrait-io/substrait-go/v8/types"
	"google.golang.org/protobuf/encoding/protojson"
)

const regexOp = "regex"

type Options struct {
	File, Column, Op string
	Pattern          string
	Value            int64
	BatchSize        int64
	Logger           *slog.Logger // Optional; nil disables query logs.
	Explain          bool
}

// Run builds a Substrait plan from the file schema, then explains or executes it.
// Output is newline-delimited JSON; callers may receive partial output on error.
func Run(ctx context.Context, opts Options, out io.Writer) (err error) {
	started := time.Now()
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	var batches, scanned, matched, written int64
	defer func() {
		elapsed := time.Since(started)
		status := "ok"
		if err != nil {
			status = "failed"
		}
		if opts.Explain {
			logger.InfoContext(ctx, "plan finished", slog.String("status", status), slog.Duration("elapsed", elapsed))
			return
		}
		var rate float64
		if elapsed > 0 {
			rate = float64(scanned) / elapsed.Seconds()
		}
		logger.InfoContext(ctx, "query finished", slog.String("status", status),
			slog.Duration("elapsed", elapsed), slog.Int64("batches", batches),
			slog.Int64("rows_scanned", scanned), slog.Int64("rows_matched", matched),
			slog.Int64("rows_written", written), slog.Float64("rows_per_second", rate))
	}()
	logger.DebugContext(ctx, "starting query", slog.String("file", opts.File),
		slog.String("column", opts.Column), slog.String("operator", opts.Op), slog.Int64("batch_size", opts.BatchSize))

	if opts.BatchSize <= 0 {
		return fmt.Errorf("batch size must be positive")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("start query: %w", err)
	}
	pf, err := file.OpenParquetFile(opts.File, false)
	if err != nil {
		return fmt.Errorf("open parquet %q: %w", opts.File, err)
	}
	defer func() {
		if closeErr := pf.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close parquet %q: %w", opts.File, closeErr))
		}
	}()
	reader, err := pqarrow.NewFileReader(pf, pqarrow.ArrowReadProperties{BatchSize: opts.BatchSize}, memory.DefaultAllocator)
	if err != nil {
		return fmt.Errorf("create Arrow reader for %q: %w", opts.File, err)
	}
	schema, err := reader.Schema()
	if err != nil {
		return fmt.Errorf("read schema from %q: %w", opts.File, err)
	}
	logger.DebugContext(ctx, "read parquet schema", slog.Int("columns", len(schema.Fields())))
	p, err := buildPlan(schema, opts)
	if err != nil {
		return fmt.Errorf("build Substrait plan: %w", err)
	}
	filter := p.GetRoots()[0].Input().(*plan.FilterRel)
	fn := filter.Condition().(*expr.ScalarFunction)
	matches, err := compilePredicate(fn)
	if err != nil {
		return fmt.Errorf("compile predicate for column %q: %w", opts.Column, err)
	}
	logger.DebugContext(ctx, "compiled query plan", slog.Duration("setup_elapsed", time.Since(started)))
	if opts.Explain {
		pb, err := p.ToProto()
		if err != nil {
			return fmt.Errorf("convert Substrait plan to protobuf: %w", err)
		}
		data, err := (protojson.MarshalOptions{Indent: "  ", UseProtoNames: true}).Marshal(pb)
		if err != nil {
			return fmt.Errorf("marshal Substrait plan JSON: %w", err)
		}
		if _, err := fmt.Fprintln(out, string(data)); err != nil {
			return fmt.Errorf("write Substrait plan JSON: %w", err)
		}
		return nil
	}
	rr, err := reader.GetRecordReader(ctx, nil, nil)
	if err != nil {
		return fmt.Errorf("create record reader for %q: %w", opts.File, err)
	}
	defer rr.Release()
	enc := json.NewEncoder(out)
	for rr.Next() {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("scan parquet %q: %w", opts.File, err)
		}
		record := rr.RecordBatch()
		batches++
		logger.DebugContext(ctx, "read Arrow batch", slog.Int64("batch", batches), slog.Int64("rows", record.NumRows()))
		for row := range int(record.NumRows()) {
			scanned++
			// Substrait comparisons propagate null; WHERE only keeps true.
			if !matches(record, row) {
				continue
			}
			matched++
			result := make(map[string]any, record.NumCols())
			for i, f := range schema.Fields() {
				result[f.Name] = record.Column(i).GetOneForMarshal(row)
			}
			if err := enc.Encode(result); err != nil {
				return fmt.Errorf("write row: %w", err)
			}
			written++
		}
	}
	if err := rr.Err(); err != nil {
		return fmt.Errorf("read records from %q: %w", opts.File, err)
	}
	return nil
}

func buildPlan(schema *arrow.Schema, opts Options) (*plan.Plan, error) {
	ops := map[string]string{"eq": "equal", "ne": "not_equal", "gt": "gt", "ge": "gte", "lt": "lt", "le": "lte", regexOp: "go_regexp_match"}
	op, ok := ops[opts.Op]
	if !ok {
		return nil, fmt.Errorf("unsupported operator %q (use eq, ne, gt, ge, lt, le, regex)", opts.Op)
	}
	indices := schema.FieldIndices(opts.Column)
	if len(indices) != 1 {
		names := make([]string, len(schema.Fields()))
		for i, f := range schema.Fields() {
			names[i] = f.Name
		}
		return nil, fmt.Errorf("column %q must exist and be unique (available columns: %q; names are case-sensitive)", opts.Column, names)
	}
	wantType := arrow.INT64
	if opts.Op == regexOp {
		wantType = arrow.STRING
	}
	if schema.Field(indices[0]).Type.ID() != wantType {
		return nil, fmt.Errorf("operator %q requires %s column, got %s for %q", opts.Op, wantType, schema.Field(indices[0]).Type, opts.Column)
	}
	ns := types.NamedStruct{Struct: types.StructType{Nullability: types.NullabilityRequired}}
	seen := make(map[string]bool)
	for _, f := range schema.Fields() {
		if seen[f.Name] {
			return nil, fmt.Errorf("duplicate column name %q", f.Name)
		}
		seen[f.Name] = true
		n := types.NullabilityRequired
		if f.Nullable {
			n = types.NullabilityNullable
		}
		var t types.Type
		switch f.Type.ID() {
		case arrow.INT64:
			t = &types.Int64Type{Nullability: n}
		case arrow.INT32:
			t = &types.Int32Type{Nullability: n}
		case arrow.STRING:
			t = &types.StringType{Nullability: n}
		case arrow.BOOL:
			t = &types.BooleanType{Nullability: n}
		case arrow.FLOAT64:
			t = &types.Float64Type{Nullability: n}
		default:
			return nil, fmt.Errorf("unsupported type %s for column %q", f.Type, f.Name)
		}
		ns.Names = append(ns.Names, f.Name)
		ns.Struct.Types = append(ns.Struct.Types, t)
	}
	b := plan.NewBuilderDefault()
	namespace := extensions.SubstraitDefaultURNPrefix + "functions_comparison"
	if opts.Op == regexOp {
		collection, err := regexCollection()
		if err != nil {
			return nil, err
		}
		b = plan.NewBuilder(collection)
		namespace = regexURN
	}
	scan := b.NamedScan([]string{opts.File}, ns)
	columnIndex := indices[0]
	if columnIndex < 0 || columnIndex > math.MaxInt32 {
		return nil, fmt.Errorf("column index %d is outside Substrait int32 range", columnIndex)
	}
	ref, err := b.RootFieldRef(scan, int32(columnIndex))
	if err != nil {
		return nil, err
	}
	var literal expr.Literal
	if opts.Op == regexOp {
		literal, err = expr.NewLiteral(opts.Pattern, false)
	} else {
		literal, err = expr.NewLiteral(opts.Value, false)
	}
	if err != nil {
		return nil, err
	}
	condition, err := b.ScalarFn(namespace, op, nil, ref, literal)
	if err != nil {
		return nil, err
	}
	filter, err := b.Filter(scan, condition)
	if err != nil {
		return nil, err
	}
	return b.Plan(filter, ns.Names)
}

func compare(op string, left, right int64) bool {
	switch op {
	case "equal":
		return left == right
	case "not_equal":
		return left != right
	case "gt":
		return left > right
	case "gte":
		return left >= right
	case "lt":
		return left < right
	case "lte":
		return left <= right
	default:
		panic("unexpected generated comparison: " + op)
	}
}

// compilePredicate consumes only locally generated plans and compiles regex once,
// before scanning or producing explain output. Null predicates never match.
func compilePredicate(fn *expr.ScalarFunction) (func(arrow.RecordBatch, int) bool, error) {
	field := int(fn.Arg(0).(*expr.FieldReference).ToProto().GetSelection().GetDirectReference().GetStructField().GetField())
	if fn.ID().URN == regexURN && fn.Name() == "go_regexp_match" {
		pattern := fn.Arg(1).(*expr.PrimitiveLiteral[string]).Value
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid regex: %w", err)
		}
		return func(record arrow.RecordBatch, row int) bool {
			column := record.Column(field).(*array.String)
			return !column.IsNull(row) && re.MatchString(column.Value(row))
		}, nil
	}
	value := fn.Arg(1).(*expr.PrimitiveLiteral[int64]).Value
	return func(record arrow.RecordBatch, row int) bool {
		column := record.Column(field).(*array.Int64)
		return !column.IsNull(row) && compare(fn.Name(), column.Value(row), value)
	}, nil
}
