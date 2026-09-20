package integration_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wolfeidau/parquet-arrow-search/internal/filter"
	"github.com/wolfeidau/parquet-arrow-search/internal/query"
)

func TestLogSamples(t *testing.T) {
	files := []string{
		"../testdata/bash-example.parquet",
		"../testdata/bazel-bazel_build_32517_rocky-rocky-linux-8.parquet",
		"../testdata/bun_build_19487_windows-x64-build-cpp.parquet",
	}
	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			var output bytes.Buffer
			if _, err := query.Run(t.Context(), &output, query.WithFile(path), query.WithPlanBuilder(filter.Planner(filter.Config{Column: "content", Op: "regex"})), query.WithBatchSize(128)); err != nil {
				t.Fatal(err)
			}
			dec := json.NewDecoder(&output)
			rows := 0
			for dec.More() {
				var row map[string]any
				if err := dec.Decode(&row); err != nil {
					t.Fatal(err)
				}
				for name := range strings.FieldsSeq("timestamp content group flags") {
					if _, ok := row[name]; !ok {
						t.Fatalf("missing column %q", name)
					}
				}
				rows++
			}
			if rows == 0 {
				t.Fatal("expected log rows")
			}
		})
	}
}
