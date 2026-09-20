// Package schema shares flat Parquet schema validation and typed Substrait literals.
package schema

import (
	"fmt"
	"math"
	"strconv"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/substrait-io/substrait-go/v8/expr"
	"github.com/substrait-io/substrait-go/v8/types"
)

// SubstraitSchema validates unique flat column names and preserves their types and nullability.
func SubstraitSchema(schema *arrow.Schema) (types.NamedStruct, error) {
	ns := types.NamedStruct{Struct: types.StructType{Nullability: types.NullabilityRequired}}
	seen := make(map[string]bool)
	for _, f := range schema.Fields() {
		if seen[f.Name] {
			return types.NamedStruct{}, fmt.Errorf("duplicate column name %q", f.Name)
		}
		seen[f.Name] = true

		n := types.NullabilityRequired
		if f.Nullable {
			n = types.NullabilityNullable
		}

		var t types.Type
		switch f.Type.ID() {
		case arrow.INT8:
			t = &types.Int8Type{Nullability: n}
		case arrow.INT16:
			t = &types.Int16Type{Nullability: n}
		case arrow.INT64:
			t = &types.Int64Type{Nullability: n}
		case arrow.INT32:
			t = &types.Int32Type{Nullability: n}
		case arrow.STRING:
			t = &types.StringType{Nullability: n}
		case arrow.BOOL:
			t = &types.BooleanType{Nullability: n}
		case arrow.FLOAT64:
			t = &types.Float64Type{Nullability: n}
		default:
			return types.NamedStruct{}, fmt.Errorf("unsupported type %s for column %q", f.Type, f.Name)
		}

		ns.Names = append(ns.Names, f.Name)
		ns.Struct.Types = append(ns.Struct.Types, t)
	}

	return ns, nil
}

// FieldIndex resolves a unique, case-sensitive column name.
func FieldIndex(schema *arrow.Schema, name string) (int32, error) {
	indices := schema.FieldIndices(name)
	if len(indices) != 1 {
		names := make([]string, len(schema.Fields()))
		for i, field := range schema.Fields() {
			names[i] = field.Name
		}
		return 0, fmt.Errorf("column %q must exist and be unique (available columns: %q; names are case-sensitive)", name, names)
	}

	index := indices[0]
	if index < 0 || index > math.MaxInt32 {
		return 0, fmt.Errorf("column index %d is outside Substrait int32 range", index)
	}
	return int32(index), nil
}

// IntegerLiteral checks the signed integer range before constructing a literal of the column's type.
func IntegerLiteral(field arrow.Field, text string) (expr.Literal, error) {
	var bits int
	switch field.Type.ID() {
	case arrow.INT8:
		bits = 8
	case arrow.INT16:
		bits = 16
	case arrow.INT32:
		bits = 32
	case arrow.INT64:
		bits = 64
	default:
		return nil, fmt.Errorf("column %q requires a signed integer type, got %s", field.Name, field.Type)
	}

	n, err := strconv.ParseInt(text, 10, bits)
	if err != nil {
		return nil, fmt.Errorf("parse integer for column %q: %w", field.Name, err)
	}

	var literal expr.Literal
	switch bits {
	case 8:
		if n < math.MinInt8 || n > math.MaxInt8 {
			return nil, fmt.Errorf("integer out of int8 range")
		}
		literal, err = expr.NewLiteral(int8(n), false)
	case 16:
		if n < math.MinInt16 || n > math.MaxInt16 {
			return nil, fmt.Errorf("integer out of int16 range")
		}
		literal, err = expr.NewLiteral(int16(n), false)
	case 32:
		if n < math.MinInt32 || n > math.MaxInt32 {
			return nil, fmt.Errorf("integer out of int32 range")
		}
		literal, err = expr.NewLiteral(int32(n), false)
	default:
		literal, err = expr.NewLiteral(n, false)
	}
	if err != nil {
		return nil, fmt.Errorf("build integer literal for column %q: %w", field.Name, err)
	}

	return literal, nil
}

// StringLiteral validates the column type and preserves the supplied string verbatim.
func StringLiteral(field arrow.Field, value string) (expr.Literal, error) {
	if field.Type.ID() != arrow.STRING {
		return nil, fmt.Errorf("column %q requires a string type, got %s", field.Name, field.Type)
	}

	literal, err := expr.NewLiteral(value, false)
	if err != nil {
		return nil, fmt.Errorf("build string literal for column %q: %w", field.Name, err)
	}

	return literal, nil
}
