package predicate

import (
	"testing"

	"github.com/substrait-io/substrait-go/v8/expr"
	"github.com/substrait-io/substrait-go/v8/plan"
	"github.com/substrait-io/substrait-go/v8/types"
)

func TestRegexCollection(t *testing.T) {
	first, err := RegexCollection()
	if err != nil {
		t.Fatal(err)
	}
	second, err := RegexCollection()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("collections must be isolated")
	}
	b := plan.NewBuilder(first)
	input := expr.NewPrimitiveLiteral("error", false)
	pattern := expr.NewPrimitiveLiteral("err.*", false)
	fn, err := b.ScalarFn(RegexURN, RegexFunction, nil, input, pattern)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fn.GetType().(*types.BooleanType); !ok {
		t.Fatalf("return type = %s", fn.GetType())
	}
}
