package filter

import (
	"strings"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/substrait-io/substrait-go/v8/plan"
	"google.golang.org/protobuf/encoding/protojson"
)

func filterSchema() *arrow.Schema {
	return arrow.NewSchema([]arrow.Field{
		{Name: "quantity", Type: arrow.PrimitiveTypes.Int16, Nullable: true},
		{Name: "product", Type: arrow.BinaryTypes.String},
	}, nil)
}

func TestDefaultPlan(t *testing.T) {
	p, err := Planner(Config{Column: "quantity"})(filterSchema())
	if err != nil {
		t.Fatal(err)
	}
	root := p.GetRoots()[0]
	if names := root.Names(); len(names) != 2 || names[0] != "quantity" || names[1] != "product" {
		t.Fatalf("output names = %v", names)
	}
	filter, ok := root.Input().(*plan.FilterRel)
	if !ok {
		t.Fatalf("expected filter, got %T", root.Input())
	}
	if _, ok := filter.Input().(*plan.NamedTableReadRel); !ok {
		t.Fatalf("expected named scan, got %T", filter.Input())
	}
	pb, err := p.ToProto()
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(strings.Fields(protojson.Format(pb)), "")
	for _, want := range []string{`"input"`, `gte:`, `"i16":0`} {
		if !strings.Contains(text, want) {
			t.Fatalf("plan missing %q: %s", want, text)
		}
	}
}

func TestOperatorsAndLiteralTypes(t *testing.T) {
	text := "pear"
	for _, op := range []string{"eq", "ne", "gt", "ge", "lt", "le"} {
		for _, config := range []Config{
			{Column: "quantity", Op: op, Value: 2},
			{Column: "product", Op: op, StringValue: &text},
		} {
			if _, err := BuildPlan(filterSchema(), config); err != nil {
				t.Errorf("%+v: %v", config, err)
			}
		}
	}
	if _, err := BuildPlan(filterSchema(), Config{Column: "product", Op: "regex", Pattern: "pe.*"}); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidConfig(t *testing.T) {
	text := "pear"
	for _, config := range []Config{
		{},
		{Column: "missing"},
		{Column: "Quantity"},
		{Column: "quantity", Op: "bad"},
		{Column: "quantity", Value: 32768},
		{Column: "quantity", Value: -32769},
		{Column: "quantity", StringValue: &text},
		{Column: "product", Value: 1},
		{Column: "quantity", Op: "regex", Pattern: "a"},
		{Column: "product", Op: "eq", StringValue: &text, Pattern: "a"},
		{Column: "product", Op: "regex", StringValue: &text},
	} {
		if _, err := BuildPlan(filterSchema(), config); err == nil {
			t.Errorf("accepted %+v", config)
		}
	}
}

func TestInvalidSchemas(t *testing.T) {
	for _, fields := range [][]arrow.Field{
		{{Name: "quantity", Type: arrow.PrimitiveTypes.Int16}, {Name: "quantity", Type: arrow.PrimitiveTypes.Int16}},
		{{Name: "quantity", Type: arrow.PrimitiveTypes.Int16}, {Name: "nested", Type: arrow.ListOf(arrow.BinaryTypes.String)}},
	} {
		if _, err := BuildPlan(arrow.NewSchema(fields, nil), Config{Column: "quantity"}); err == nil {
			t.Fatalf("accepted schema %v", fields)
		}
	}
}
