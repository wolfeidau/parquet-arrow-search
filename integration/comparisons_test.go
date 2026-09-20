package integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"testing"

	"github.com/wolfeidau/parquet-arrow-search/internal/filter"
	"github.com/wolfeidau/parquet-arrow-search/internal/query"
)

func TestComparisons(t *testing.T) {
	path := fixture(t)
	for _, tc := range []struct {
		op   string
		want []int64
	}{
		{"eq", []int64{2}}, {"ne", []int64{1, 3}}, {"gt", []int64{3}},
		{"ge", []int64{2, 3}}, {"lt", []int64{1}}, {"le", []int64{1, 2}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			var out bytes.Buffer
			_, err := query.Run(t.Context(), &out, query.WithFile(path), query.WithPlanBuilder(filter.Planner(filter.Config{Column: "age", Op: tc.op, Value: 37})), query.WithBatchSize(2))
			if err != nil {
				t.Fatal(err)
			}
			var got []int64
			dec := json.NewDecoder(&out)
			for {
				var row struct {
					ID int64 `json:"id"`
				}
				if err := dec.Decode(&row); err == io.EOF {
					break
				} else if err != nil {
					t.Fatal(err)
				}
				got = append(got, row.ID)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
