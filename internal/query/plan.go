package query

import (
	"fmt"
	"math"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/substrait-io/substrait-go/v8/expr"
	"github.com/substrait-io/substrait-go/v8/extensions"
	"github.com/substrait-io/substrait-go/v8/plan"
	"github.com/substrait-io/substrait-go/v8/types"
)

const (
	functionEqual    = "equal"
	functionNotEqual = "not_equal"
	functionGTE      = "gte"
	functionLTE      = "lte"
)

func buildLegacyPlan(schema *arrow.Schema, opts options) (*plan.Plan, error) {
	ops := map[string]string{
		"eq":    functionEqual,
		"ne":    functionNotEqual,
		"gt":    "gt",
		"ge":    functionGTE,
		"lt":    "lt",
		"le":    functionLTE,
		regexOp: "go_regexp_match",
	}
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
	ns, err := substraitSchema(schema)
	if err != nil {
		return nil, fmt.Errorf("convert schema: %w", err)
	}

	b := plan.NewBuilderDefault()
	namespace := extensions.SubstraitDefaultURNPrefix + "functions_comparison"
	if opts.Op == regexOp {
		collection, err := regexCollection()
		if err != nil {
			return nil, fmt.Errorf("prepare regex functions: %w", err)
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
		return nil, fmt.Errorf("reference filter column: %w", err)
	}

	var literal expr.Literal
	if opts.Op == regexOp {
		literal, err = expr.NewLiteral(opts.Pattern, false)
	} else {
		literal, err = expr.NewLiteral(opts.Value, false)
	}
	if err != nil {
		return nil, fmt.Errorf("build filter literal: %w", err)
	}

	condition, err := b.ScalarFn(namespace, op, nil, ref, literal)
	if err != nil {
		return nil, fmt.Errorf("build comparison function: %w", err)
	}

	filter, err := b.Filter(scan, condition)
	if err != nil {
		return nil, fmt.Errorf("build filter relation: %w", err)
	}

	p, err := b.Plan(filter, ns.Names)
	if err != nil {
		return nil, fmt.Errorf("build plan root: %w", err)
	}
	return p, nil
}

func substraitSchema(schema *arrow.Schema) (types.NamedStruct, error) {
	ns := types.NamedStruct{Struct: types.StructType{Nullability: types.NullabilityRequired}}
	seen := make(map[string]bool)
	for _, f := range schema.Fields() {
		if seen[f.Name] {
			return types.NamedStruct{}, fmt.Errorf("duplicate column name %q", f.Name)
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
			return types.NamedStruct{}, fmt.Errorf("unsupported type %s for column %q", f.Type, f.Name)
		}

		ns.Names = append(ns.Names, f.Name)
		ns.Struct.Types = append(ns.Struct.Types, t)
	}

	return ns, nil
}
