package sql

import (
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/substrait-io/substrait-go/v8/plan"
)

func TestPlanFiltersBeforeProjectionAndLimit(t *testing.T) {
	schema := arrow.NewSchema([]arrow.Field{
		{Name: "id", Type: arrow.PrimitiveTypes.Int64},
		{Name: "name", Type: arrow.BinaryTypes.String},
		{Name: "age", Type: arrow.PrimitiveTypes.Int64, Nullable: true},
	}, nil)
	p, err := BuildPlan(schema, "SELECT name, id FROM logs WHERE age >= 30 LIMIT 2")
	if err != nil {
		t.Fatal(err)
	}
	root := p.GetRoots()[0]
	if names := root.Names(); len(names) != 2 || names[0] != "name" || names[1] != "id" {
		t.Fatalf("output names = %v", names)
	}
	fetch, ok := root.Input().(*plan.FetchRel)
	if !ok {
		t.Fatalf("expected fetch, got %T", root.Input())
	}
	if fetch.Count() != 2 || fetch.Offset() != 0 {
		t.Fatalf("unexpected fetch: %v", fetch)
	}
	project, ok := fetch.Input().(*plan.ProjectRel)
	if !ok {
		t.Fatalf("expected project, got %T", fetch.Input())
	}
	if got := project.OutputMapping(); len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Fatalf("unexpected output mapping: %v", got)
	}
	filter, ok := project.Input().(*plan.FilterRel)
	if !ok {
		t.Fatalf("expected filter, got %T", project.Input())
	}
	if _, ok := filter.Input().(*plan.NamedTableReadRel); !ok {
		t.Fatalf("expected read, got %T", filter.Input())
	}
}
