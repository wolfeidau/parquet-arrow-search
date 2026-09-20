package query

import (
	"fmt"
	"strconv"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/substrait-io/substrait-go/v8/expr"
	"github.com/substrait-io/substrait-go/v8/extensions"
	"github.com/substrait-io/substrait-go/v8/plan"
	"github.com/wolfeidau/parquet-arrow-search/internal/predicate"
	"github.com/wolfeidau/parquet-arrow-search/internal/schema"
)

const (
	functionEqual    = "equal"
	functionNotEqual = "not_equal"
	functionGTE      = "gte"
	functionLTE      = "lte"
)

func buildFilterPlan(fileSchema *arrow.Schema, opts options) (*plan.Plan, error) {
	ops := map[string]string{
		"eq":    functionEqual,
		"ne":    functionNotEqual,
		"gt":    "gt",
		"ge":    functionGTE,
		"lt":    "lt",
		"le":    functionLTE,
		regexOp: predicate.RegexFunction,
	}
	op, ok := ops[opts.Op]
	if !ok {
		return nil, fmt.Errorf("unsupported operator %q (use eq, ne, gt, ge, lt, le, regex)", opts.Op)
	}

	index, err := schema.FieldIndex(fileSchema, opts.Column)
	if err != nil {
		return nil, fmt.Errorf("resolve filter column: %w", err)
	}
	field := fileSchema.Field(int(index))
	ns, err := schema.SubstraitSchema(fileSchema)
	if err != nil {
		return nil, fmt.Errorf("convert schema: %w", err)
	}

	b := plan.NewBuilderDefault()
	namespace := extensions.SubstraitDefaultURNPrefix + "functions_comparison"
	if opts.Op == regexOp {
		collection, err := predicate.RegexCollection()
		if err != nil {
			return nil, fmt.Errorf("prepare regex functions: %w", err)
		}
		b = plan.NewBuilder(collection)
		namespace = predicate.RegexURN
	}

	scan := b.NamedScan([]string{opts.File}, ns)
	ref, err := b.RootFieldRef(scan, index)
	if err != nil {
		return nil, fmt.Errorf("reference filter column: %w", err)
	}

	var literal expr.Literal
	switch {
	case opts.Op == regexOp:
		literal, err = schema.StringLiteral(field, opts.Pattern)
	case opts.StringValue != nil:
		literal, err = schema.StringLiteral(field, *opts.StringValue)
	default:
		literal, err = schema.IntegerLiteral(field, strconv.FormatInt(opts.Value, 10))
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

func buildPlan(fileSchema *arrow.Schema, opts options) (*plan.Plan, error) {
	if opts.PlanBuilder != nil {
		p, err := opts.PlanBuilder(fileSchema)
		if err != nil {
			return nil, fmt.Errorf("build custom plan: %w", err)
		}
		if p == nil {
			return nil, fmt.Errorf("plan builder returned a nil plan")
		}
		return p, nil
	}
	p, err := buildFilterPlan(fileSchema, opts)
	if err != nil {
		return nil, fmt.Errorf("build filter plan: %w", err)
	}
	return p, nil
}
