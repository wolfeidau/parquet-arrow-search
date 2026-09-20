package sql

import (
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/substrait-io/substrait-go/v8/plan"
)

func TestParsePreservesQuotedNamesAndRegex(t *testing.T) {
	q, err := parseQuery(`select "odd""name" from logs where regex("product name", '\bO''Brien\b') limit 0;`)
	if err != nil {
		t.Fatal(err)
	}
	if q.Select.Columns[0].name() != `odd"name` || q.Where.Regex.Column.name() != "product name" || unquoteSQL(q.Where.Regex.Pattern) != `\bO'Brien\b` {
		t.Fatalf("quoted text changed: %+v", q)
	}
	limit, err := q.rowLimit()
	if err != nil || limit != 0 {
		t.Fatalf("limit = %d, %v", limit, err)
	}
}

func TestBuildPlanValidatesGenericSchema(t *testing.T) {
	schema := arrow.NewSchema([]arrow.Field{
		{Name: "sku", Type: arrow.BinaryTypes.String},
		{Name: "stock", Type: arrow.PrimitiveTypes.Int16, Nullable: true},
	}, nil)
	p, err := BuildPlan(schema, `SELECT sku FROM logs WHERE stock >= 2 LIMIT 3`)
	if err != nil {
		t.Fatal(err)
	}
	root := p.GetRoots()[0]
	if names := root.Names(); len(names) != 1 || names[0] != "sku" {
		t.Fatalf("output names %v", names)
	}
	fetch, ok := root.Input().(*plan.FetchRel)
	if !ok || fetch.Count() != 3 {
		t.Fatalf("unexpected root %v", root.Input())
	}
	for _, text := range []string{
		`SELECT Stock FROM logs`,
		`SELECT sku, sku FROM logs`,
		`SELECT * FROM logs WHERE stock = 32768`,
		`SELECT * FROM logs WHERE stock = '2'`,
		`SELECT * FROM logs WHERE sku = 2`,
		`SELECT * FROM logs WHERE regex(stock, '2')`,
		`SELECT * FROM other`,
		`SELECT * FROM logs LIMIT -1`,
		`SELECT * FROM logs WHERE stock > 1 AND stock < 3`,
	} {
		if _, err := BuildPlan(schema, text); err == nil {
			t.Errorf("accepted %s", text)
		}
	}
}
