package main

import (
	"io"
	"testing"

	"github.com/alecthomas/kong"
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
	if c.Op != "ge" || c.BatchSize != 65536 || c.Value != 0 || c.Debug || c.Explain {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	c, err = parseCLI(t, "--file=logs.parquet", "--column=content", "--op=regex", "--pattern=(?i)error|failed", "--value=-42", "--batch-size=2", "--explain", "--debug")
	if err != nil {
		t.Fatal(err)
	}
	o := c
	if o.File != "logs.parquet" || o.Column != "content" || o.Op != "regex" || o.Pattern != "(?i)error|failed" || o.Value != -42 || o.BatchSize != 2 || !o.Explain || !c.Debug {
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
