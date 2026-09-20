package schema

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/substrait-io/substrait-go/v8/types"
)

func TestSubstraitSchema(t *testing.T) {
	fields := []arrow.Field{
		{Name: "tiny", Type: arrow.PrimitiveTypes.Int8},
		{Name: "small", Type: arrow.PrimitiveTypes.Int16, Nullable: true},
		{Name: "medium", Type: arrow.PrimitiveTypes.Int32},
		{Name: "large", Type: arrow.PrimitiveTypes.Int64, Nullable: true},
		{Name: "message", Type: arrow.BinaryTypes.String, Nullable: true},
		{Name: "enabled", Type: arrow.FixedWidthTypes.Boolean},
		{Name: "ratio", Type: arrow.PrimitiveTypes.Float64},
	}
	want := []string{"i8", "i16", "i32", "i64", "str", "bool", "fp64"}
	converted, err := SubstraitSchema(arrow.NewSchema(fields, nil))
	if err != nil {
		t.Fatal(err)
	}
	for i, field := range fields {
		if converted.Names[i] != field.Name {
			t.Fatalf("field %d name = %q", i, converted.Names[i])
		}
		typ := converted.Struct.Types[i]
		if strings.TrimSuffix(typ.ShortString(), "?") != want[i] {
			t.Fatalf("field %q type = %s", field.Name, typ)
		}
		nullable := types.NullabilityRequired
		if field.Nullable {
			nullable = types.NullabilityNullable
		}
		if typ.GetNullability() != nullable {
			t.Fatalf("field %q nullability = %v", field.Name, typ.GetNullability())
		}
	}
}

func TestRejectUnsupportedSchema(t *testing.T) {
	for _, typ := range []arrow.DataType{arrow.PrimitiveTypes.Uint8, arrow.PrimitiveTypes.Uint16, arrow.PrimitiveTypes.Uint32, arrow.PrimitiveTypes.Uint64, arrow.ListOf(arrow.BinaryTypes.String), arrow.StructOf(arrow.Field{Name: "child", Type: arrow.PrimitiveTypes.Int64})} {
		t.Run(typ.String(), func(t *testing.T) {
			_, err := SubstraitSchema(arrow.NewSchema([]arrow.Field{{Name: "value", Type: typ}}, nil))
			if err == nil || !strings.Contains(err.Error(), "unsupported type") {
				t.Fatalf("error = %v", err)
			}
		})
	}
	duplicate := arrow.NewSchema([]arrow.Field{{Name: "a", Type: arrow.BinaryTypes.String}, {Name: "a", Type: arrow.BinaryTypes.String}}, nil)
	if _, err := SubstraitSchema(duplicate); err == nil {
		t.Fatal("accepted duplicate names")
	}
	if _, err := FieldIndex(duplicate, "a"); err == nil {
		t.Fatal("resolved duplicate name")
	}
}

func TestFieldIndex(t *testing.T) {
	schema := arrow.NewSchema([]arrow.Field{{Name: "ID", Type: arrow.PrimitiveTypes.Int64}, {Name: "id", Type: arrow.BinaryTypes.String}}, nil)
	for name, want := range map[string]int32{"ID": 0, "id": 1} {
		got, err := FieldIndex(schema, name)
		if err != nil || got != want {
			t.Fatalf("FieldIndex(%q) = %d, %v", name, got, err)
		}
	}
	if _, err := FieldIndex(schema, "Id"); err == nil {
		t.Fatal("accepted incorrect case")
	}
}

func TestIntegerLiteral(t *testing.T) {
	for _, tc := range []struct {
		typ                   arrow.DataType
		min, max, under, over string
	}{
		{arrow.PrimitiveTypes.Int8, "-128", "127", "-129", "128"},
		{arrow.PrimitiveTypes.Int16, "-32768", "32767", "-32769", "32768"},
		{arrow.PrimitiveTypes.Int32, "-2147483648", "2147483647", "-2147483649", "2147483648"},
		{arrow.PrimitiveTypes.Int64, "-9223372036854775808", "9223372036854775807", "-9223372036854775809", "9223372036854775808"},
	} {
		t.Run(tc.typ.String(), func(t *testing.T) {
			field := arrow.Field{Name: "number", Type: tc.typ, Nullable: true}
			schema, err := SubstraitSchema(arrow.NewSchema([]arrow.Field{field}, nil))
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range []string{tc.min, "0", tc.max} {
				literal, err := IntegerLiteral(field, value)
				if err != nil {
					t.Fatal(err)
				}
				if literal.ValueString() != value {
					t.Fatalf("literal = %s, want %s", literal.ValueString(), value)
				}
				wantType := schema.Struct.Types[0].WithNullability(types.NullabilityRequired)
				if !literal.GetType().Equals(wantType) {
					t.Fatalf("type = %s, want %s", literal.GetType(), wantType)
				}
			}
			for _, value := range []string{tc.under, tc.over, "1.5", "abc"} {
				_, err := IntegerLiteral(field, value)
				var numberErr *strconv.NumError
				if !errors.As(err, &numberErr) {
					t.Fatalf("expected wrapped parse error for %q, got %v", value, err)
				}
			}
		})
	}
	for _, typ := range []arrow.DataType{arrow.BinaryTypes.String, arrow.PrimitiveTypes.Uint64, arrow.PrimitiveTypes.Float64} {
		if _, err := IntegerLiteral(arrow.Field{Name: "value", Type: typ}, "1"); err == nil {
			t.Fatalf("accepted %s", typ)
		}
	}
}

func TestStringLiteral(t *testing.T) {
	field := arrow.Field{Name: "message", Type: arrow.BinaryTypes.String, Nullable: true}
	value := `error\s+'quoted'`
	literal, err := StringLiteral(field, value)
	if err != nil {
		t.Fatal(err)
	}
	if literal.ToProtoLiteral().GetString_() != value {
		t.Fatalf("string changed: %s", literal)
	}
	if literal.GetType().GetNullability() != types.NullabilityRequired {
		t.Fatal("literal must be required")
	}
	if _, err := StringLiteral(arrow.Field{Name: "number", Type: arrow.PrimitiveTypes.Int64}, "1"); err == nil {
		t.Fatal("accepted string for integer column")
	}
}
