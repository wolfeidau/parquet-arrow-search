package query

import (
	"testing"

	"github.com/substrait-io/substrait-go/v8/expr"
	"github.com/substrait-io/substrait-go/v8/plan"
	"github.com/wolfeidau/parquet-arrow-search/internal/schema"
)

func TestCompileProjectionAndLimit(t *testing.T) {
	fileSchema := peopleSchema()
	ns, err := schema.SubstraitSchema(fileSchema)
	if err != nil {
		t.Fatal(err)
	}

	builder := plan.NewBuilderDefault()
	read := builder.NamedScan([]string{"people"}, ns)
	var refs []expr.Expression
	for _, index := range []int32{1, 0} {
		ref, err := builder.RootFieldRef(read, index)
		if err != nil {
			t.Fatal(err)
		}
		refs = append(refs, ref)
	}

	project, err := builder.Project(read, refs...)
	if err != nil {
		t.Fatal(err)
	}

	projected, err := project.Remap(3, 4)
	if err != nil {
		t.Fatal(err)
	}

	fetch, err := builder.Fetch(projected, 0, 2)
	if err != nil {
		t.Fatal(err)
	}

	p, err := builder.Plan(fetch, []string{"name", "id"})
	if err != nil {
		t.Fatal(err)
	}

	execution, err := compilePlan(p, fileSchema)
	if err != nil {
		t.Fatal(err)
	}
	if execution.limit != 2 || len(execution.columns) != 2 || execution.columns[0] != 1 || execution.columns[1] != 0 {
		t.Fatalf("execution: %+v", execution)
	}
}
