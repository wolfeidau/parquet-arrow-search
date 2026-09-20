// Package filter builds Substrait plans from a single column filter.
package filter

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
	regexOp          = "regex"
	functionEqual    = "equal"
	functionNotEqual = "not_equal"
	functionGTE      = "gte"
	functionLTE      = "lte"
)

// Config describes one typed comparison or regular expression filter.
// An empty Op defaults to ge; Value defaults to zero.
// StringValue, when non-nil, replaces Value for comparisons.
// Regex uses Pattern and ignores Value; StringValue cannot be set with regex.
type Config struct {
	Column      string
	Op          string
	Pattern     string
	Value       int64
	StringValue *string
}

// Planner binds a filter configuration for use with a schema-based query runner.
func Planner(config Config) func(*arrow.Schema) (*plan.Plan, error) {
	return func(fileSchema *arrow.Schema) (*plan.Plan, error) {
		return BuildPlan(fileSchema, config)
	}
}

// BuildPlan validates the column and literal and creates a single-table filter plan.
func BuildPlan(fileSchema *arrow.Schema, opts Config) (*plan.Plan, error) {
	if opts.Column == "" {
		return nil, fmt.Errorf("filter column is required")
	}
	if opts.Op == "" {
		opts.Op = "ge"
	}
	if opts.Op != regexOp && opts.Pattern != "" {
		return nil, fmt.Errorf("pattern requires the regex operator")
	}
	if opts.Op == regexOp && opts.StringValue != nil {
		return nil, fmt.Errorf("regex uses pattern, not string value")
	}

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

	scan := b.NamedScan([]string{"input"}, ns)
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
