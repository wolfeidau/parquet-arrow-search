package main

import (
	"bytes"
	"io"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/wolfeidau/parquet-arrow-search/internal/query"
)

func parseCLI(t *testing.T, args ...string) (cli, error) {
	t.Helper()
	var c cli
	p, err := kong.New(&c, kong.Writers(io.Discard, io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Parse(args)
	return c, err
}

func TestCLIOptions(t *testing.T) {
	c, err := parseCLI(t, "--file", "logs.parquet", "--column", "timestamp")
	if err != nil {
		t.Fatal(err)
	}
	if c.Op != nil || c.BatchSize != 65536 || c.Value != nil || c.Debug || c.Explain {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	c, err = parseCLI(t, "--file=logs.parquet", "--column=content", "--op=regex", "--pattern=(?i)error|failed", "--value=-42", "--batch-size=2", "--explain", "--debug")
	if err != nil {
		t.Fatal(err)
	}
	o := c
	if o.File != "logs.parquet" || o.Column == nil || *o.Column != "content" || o.Op == nil || *o.Op != "regex" || o.Pattern == nil || *o.Pattern != "(?i)error|failed" || o.Value == nil || *o.Value != -42 || o.BatchSize != 2 || !o.Explain || !c.Debug {
		t.Fatalf("incorrect options: %+v", o)
	}
}

func TestCLIValidation(t *testing.T) {
	for _, args := range [][]string{
		{}, {"--file=logs.parquet"}, {"--column=content"},
		{"--file=logs.parquet", "--column=content", "--op=unknown"},
		{"--file=logs.parquet", "--column=content", "--batch-size=0"},
		{"--file=logs.parquet", "--column=content", "--batch-size=-1"},
		{"--file=logs.parquet", "--column=content", "unexpected"},
		{"--file=logs.parquet", "--column=content", "--unknown"},
		{"--file=", "--column=content"},
	} {
		if _, err := parseCLI(t, args...); err == nil {
			t.Errorf("accepted invalid arguments: %v", args)
		}
	}
}

func TestCLIQueryMode(t *testing.T) {
	c, err := parseCLI(t, "--file=../../testdata/bash-example.parquet", "--query=SELECT content FROM logs LIMIT 2")
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := query.Run(t.Context(), &out, c.options(nil)...); err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(out.Bytes(), []byte("\n")); got != 2 {
		t.Fatalf("got %d rows, want 2", got)
	}

	for _, extra := range []string{"--column=content", "--column=", "--op=ge", "--pattern=", "--value=0"} {
		if _, err := parseCLI(t, "--file=logs.parquet", "--query=SELECT * FROM logs", extra); err == nil {
			t.Errorf("accepted mixed modes with %s", extra)
		}
	}
	if _, err := parseCLI(t, "--file=logs.parquet", "--query= "); err == nil {
		t.Fatal("accepted empty query")
	}
}
