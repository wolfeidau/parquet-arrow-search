// Package predicate defines functions shared by flag and SQL query plans.
package predicate

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/substrait-io/substrait-go/v8/extensions"
)

const RegexURN = "extension:github.com/wolfeidau/parquet-arrow-search:functions_regex"

const RegexFunction = "go_regexp_match"

//go:embed functions_regex.yaml
var regexDefinition string

// RegexCollection loads an isolated collection of regex extension functions.
func RegexCollection() (*extensions.Collection, error) {
	// Keep the custom definition separate from the shared default collection.
	var c extensions.Collection
	if err := c.Load("functions_regex.yaml", strings.NewReader(regexDefinition)); err != nil {
		return nil, fmt.Errorf("load regex extension functions_regex.yaml: %w", err)
	}
	return &c, nil
}
