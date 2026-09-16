package query

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/substrait-io/substrait-go/v8/extensions"
)

const regexURN = "extension:github.com/wolfeidau/parquet-arrow-search:functions_regex"

//go:embed functions_regex.yaml
var regexDefinition string

func regexCollection() (*extensions.Collection, error) {
	// Keep the custom definition separate from the shared default collection.
	var c extensions.Collection
	if err := c.Load("functions_regex.yaml", strings.NewReader(regexDefinition)); err != nil {
		return nil, fmt.Errorf("load regex extension functions_regex.yaml: %w", err)
	}
	return &c, nil
}
