package sql

import (
	"fmt"
	"math"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/substrait-io/substrait-go/v8/expr"
	"github.com/substrait-io/substrait-go/v8/extensions"
	"github.com/substrait-io/substrait-go/v8/plan"
	"github.com/wolfeidau/parquet-arrow-search/internal/predicate"
	"github.com/wolfeidau/parquet-arrow-search/internal/schema"
)

// BuildPlan validates a query against the file schema and builds its execution plan.
func BuildPlan(fileSchema *arrow.Schema, text string) (*plan.Plan, error) {
	q, err := parseQuery(text)
	if err != nil {
		return nil, fmt.Errorf("parse SQL: %w", err)
	}

	limit, err := q.rowLimit()
	if err != nil {
		return nil, fmt.Errorf("validate LIMIT: %w", err)
	}

	ns, err := schema.SubstraitSchema(fileSchema)
	if err != nil {
		return nil, fmt.Errorf("convert schema: %w", err)
	}

	b := plan.NewBuilderDefault()
	if q.Where != nil && q.Where.Regex != nil {
		collection, err := predicate.RegexCollection()
		if err != nil {
			return nil, fmt.Errorf("prepare regex functions: %w", err)
		}
		b = plan.NewBuilder(collection)
	}

	var rel plan.Rel = b.NamedScan([]string{"logs"}, ns)
	if q.Where != nil {
		condition, err := buildSQLPredicate(b, rel, fileSchema, q.Where)
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
		rel, names, err = buildProjection(b, rel, fileSchema, q.Select.Columns)
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

func buildProjection(b plan.Builder, input plan.Rel, fileSchema *arrow.Schema, columns []identifier) (plan.Rel, []string, error) {
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

		index, err := schema.FieldIndex(fileSchema, name)
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

func buildSQLPredicate(b plan.Builder, input plan.Rel, fileSchema *arrow.Schema, p *sqlPredicate) (*expr.ScalarFunction, error) {
	name := ""
	if p.Regex != nil {
		name = p.Regex.Column.name()
	} else {
		name = p.Column.Column.name()
	}

	index, err := schema.FieldIndex(fileSchema, name)
	if err != nil {
		return nil, fmt.Errorf("resolve predicate column: %w", err)
	}

	ref, err := b.RootFieldRef(input, index)
	if err != nil {
		return nil, fmt.Errorf("reference column %q: %w", name, err)
	}

	namespace := extensions.SubstraitDefaultURNPrefix + "functions_comparison"
	if p.Regex != nil {
		literal, err := schema.StringLiteral(fileSchema.Field(int(index)), unquoteSQL(p.Regex.Pattern))
		if err != nil {
			return nil, fmt.Errorf("build regex literal: %w", err)
		}

		fn, err := b.ScalarFn(predicate.RegexURN, predicate.RegexFunction, nil, ref, literal)
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
	literal, err := comparisonLiteral(fileSchema.Field(int(index)), comparison.Value)
	if err != nil {
		return nil, fmt.Errorf("validate comparison literal: %w", err)
	}

	functions := map[string]string{"=": "equal", "!=": "not_equal", "<>": "not_equal", "<": "lt", "<=": "lte", ">": "gt", ">=": "gte"}
	fn, err := b.ScalarFn(namespace, functions[comparison.Op], nil, ref, literal)
	if err != nil {
		return nil, fmt.Errorf("build comparison: %w", err)
	}
	return fn, nil
}

func comparisonLiteral(field arrow.Field, value sqlLiteral) (expr.Literal, error) {
	if value.String != nil {
		literal, err := schema.StringLiteral(field, unquoteSQL(*value.String))
		if err != nil {
			return nil, fmt.Errorf("build string literal: %w", err)
		}
		return literal, nil
	}
	literal, err := schema.IntegerLiteral(field, *value.Integer)
	if err != nil {
		return nil, fmt.Errorf("build integer literal: %w", err)
	}
	return literal, nil
}

// Planner binds query text to a schema-based plan builder for the shared executor.
func Planner(text string) func(*arrow.Schema) (*plan.Plan, error) {
	return func(schema *arrow.Schema) (*plan.Plan, error) {
		return BuildPlan(schema, text)
	}
}
