package query

import (
	"fmt"
	"math"
	"strconv"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/substrait-io/substrait-go/v8/expr"
	"github.com/substrait-io/substrait-go/v8/extensions"
	"github.com/substrait-io/substrait-go/v8/plan"
)

func buildPlan(schema *arrow.Schema, opts options) (*plan.Plan, error) {
	if opts.Query == nil {
		return buildLegacyPlan(schema, opts)
	}

	q, err := parseQuery(*opts.Query)
	if err != nil {
		return nil, fmt.Errorf("parse SQL: %w", err)
	}

	limit, err := q.rowLimit()
	if err != nil {
		return nil, fmt.Errorf("validate LIMIT: %w", err)
	}

	ns, err := substraitSchema(schema)
	if err != nil {
		return nil, fmt.Errorf("convert schema: %w", err)
	}

	b := plan.NewBuilderDefault()
	if q.Where != nil && q.Where.Regex != nil {
		collection, err := regexCollection()
		if err != nil {
			return nil, fmt.Errorf("prepare regex functions: %w", err)
		}
		b = plan.NewBuilder(collection)
	}

	var rel plan.Rel = b.NamedScan([]string{"logs"}, ns)
	if q.Where != nil {
		condition, err := buildSQLPredicate(b, rel, schema, q.Where)
		if err != nil {
			return nil, fmt.Errorf("build WHERE: %w", err)
		}

		rel, err = b.Filter(rel, condition)
		if err != nil {
			return nil, fmt.Errorf("build filter: %w", err)
		}
	}

	names := ns.Names
	if !q.Select.All {
		rel, names, err = buildProjection(b, rel, schema, q.Select.Columns)
		if err != nil {
			return nil, fmt.Errorf("build SELECT: %w", err)
		}
	}

	if limit >= 0 {
		rel, err = b.Fetch(rel, 0, limit)
		if err != nil {
			return nil, fmt.Errorf("build LIMIT: %w", err)
		}
	}

	p, err := b.Plan(rel, names)
	if err != nil {
		return nil, fmt.Errorf("build plan root: %w", err)
	}
	return p, nil
}

func fieldIndex(schema *arrow.Schema, name string) (int32, error) {
	indices := schema.FieldIndices(name)
	if len(indices) != 1 {
		return 0, fmt.Errorf("column %q must exist and be unique (names are case-sensitive)", name)
	}

	i := indices[0]
	if i < 0 || i > math.MaxInt32 {
		return 0, fmt.Errorf("column index %d is outside Substrait int32 range", i)
	}
	return int32(i), nil
}

func buildProjection(b plan.Builder, input plan.Rel, schema *arrow.Schema, columns []identifier) (plan.Rel, []string, error) {
	names := make([]string, 0, len(columns))
	refs := make([]expr.Expression, 0, len(columns))
	mapping := make([]int32, 0, len(columns))
	seen := make(map[string]bool)

	for _, column := range columns {
		name := column.name()
		if seen[name] {
			return nil, nil, fmt.Errorf("duplicate selected column %q", name)
		}
		seen[name] = true

		index, err := fieldIndex(schema, name)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve selected column: %w", err)
		}

		ref, err := b.RootFieldRef(input, index)
		if err != nil {
			return nil, nil, fmt.Errorf("reference column %q: %w", name, err)
		}

		// Project appends expressions to its input; emit only the appended fields.
		outputIndex := int64(input.RecordType().FieldCount()) + int64(len(refs))
		if outputIndex < 0 || outputIndex > math.MaxInt32 {
			return nil, nil, fmt.Errorf("projection exceeds Substrait int32 range")
		}
		mapping = append(mapping, int32(outputIndex))
		refs = append(refs, ref)
		names = append(names, name)
	}

	project, err := b.Project(input, refs...)
	if err != nil {
		return nil, nil, fmt.Errorf("create projection: %w", err)
	}

	rel, err := project.Remap(mapping...)
	if err != nil {
		return nil, nil, fmt.Errorf("select projection outputs: %w", err)
	}
	return rel, names, nil
}

func buildSQLPredicate(b plan.Builder, input plan.Rel, schema *arrow.Schema, p *sqlPredicate) (*expr.ScalarFunction, error) {
	name := ""
	if p.Regex != nil {
		name = p.Regex.Column.name()
	} else {
		name = p.Column.Column.name()
	}

	index, err := fieldIndex(schema, name)
	if err != nil {
		return nil, fmt.Errorf("resolve predicate column: %w", err)
	}

	ref, err := b.RootFieldRef(input, index)
	if err != nil {
		return nil, fmt.Errorf("reference column %q: %w", name, err)
	}

	namespace := extensions.SubstraitDefaultURNPrefix + "functions_comparison"
	if p.Regex != nil {
		if schema.Field(int(index)).Type.ID() != arrow.STRING {
			return nil, fmt.Errorf("regex requires a string column, got %s", schema.Field(int(index)).Type)
		}

		literal, err := expr.NewLiteral(unquoteSQL(p.Regex.Pattern), false)
		if err != nil {
			return nil, fmt.Errorf("build regex literal: %w", err)
		}

		fn, err := b.ScalarFn(regexURN, "go_regexp_match", nil, ref, literal)
		if err != nil {
			return nil, fmt.Errorf("build regex function: %w", err)
		}
		return fn, nil
	}

	if p.Column.Null != nil {
		function := "is_null"
		if p.Column.Null.Not {
			function = "is_not_null"
		}

		fn, err := b.ScalarFn(namespace, function, nil, ref)
		if err != nil {
			return nil, fmt.Errorf("build null test: %w", err)
		}
		return fn, nil
	}

	comparison := p.Column.Comparison
	literal, err := comparisonLiteral(schema.Field(int(index)), comparison.Value)
	if err != nil {
		return nil, fmt.Errorf("validate comparison literal: %w", err)
	}

	functions := map[string]string{"=": functionEqual, "!=": functionNotEqual, "<>": functionNotEqual, "<": "lt", "<=": functionLTE, ">": "gt", ">=": functionGTE}
	fn, err := b.ScalarFn(namespace, functions[comparison.Op], nil, ref, literal)
	if err != nil {
		return nil, fmt.Errorf("build comparison: %w", err)
	}
	return fn, nil
}

func comparisonLiteral(field arrow.Field, value sqlLiteral) (expr.Literal, error) {
	var literal expr.Literal
	var err error

	switch field.Type.ID() {
	case arrow.STRING:
		if value.String == nil {
			return nil, fmt.Errorf("column %q requires a string literal", field.Name)
		}
		literal, err = expr.NewLiteral(unquoteSQL(*value.String), false)
	case arrow.INT32, arrow.INT64:
		if value.Integer == nil {
			return nil, fmt.Errorf("column %q requires an integer literal", field.Name)
		}

		bits := 64
		if field.Type.ID() == arrow.INT32 {
			bits = 32
		}
		n, parseErr := strconv.ParseInt(*value.Integer, 10, bits)
		if parseErr != nil {
			return nil, fmt.Errorf("parse integer for column %q: %w", field.Name, parseErr)
		}

		if bits == 32 {
			// ParseInt has checked the target range; make it explicit for static analysis.
			if n < math.MinInt32 || n > math.MaxInt32 {
				return nil, fmt.Errorf("integer out of int32 range")
			}
			literal, err = expr.NewLiteral(int32(n), false)
		} else {
			literal, err = expr.NewLiteral(n, false)
		}
	default:
		return nil, fmt.Errorf("comparisons support int32, int64, and string columns; %q is %s", field.Name, field.Type)
	}

	if err != nil {
		return nil, fmt.Errorf("build literal for column %q: %w", field.Name, err)
	}
	return literal, nil
}
