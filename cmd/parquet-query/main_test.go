package main

import (
	"bytes"
	"io"
	"strings"
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
	c, err := parseCLI(t, "filter", "--file", "logs.parquet", "--column", "timestamp")
	if err != nil {
		t.Fatal(err)
	}
	if c.Filter.Op != "ge" || c.BatchSize != 65536 || c.Filter.Value != nil || c.Debug || c.Explain {
		t.Fatalf("unexpected defaults: %+v", c)
	}

	c, err = parseCLI(t, "--file=logs.parquet", "--debug", "filter", "--column=content", "--op=regex", "--pattern=(?i)error|failed", "--batch-size=2", "--explain")
	if err != nil {
		t.Fatal(err)
	}
	if c.File != "logs.parquet" || c.Filter.Column != "content" || c.Filter.Op != "regex" || c.Filter.Pattern == nil || *c.Filter.Pattern != "(?i)error|failed" || c.BatchSize != 2 || !c.Explain || !c.Debug {
		t.Fatalf("incorrect options: %+v", c)
	}
}

func TestCLIValidation(t *testing.T) {
	for _, args := range [][]string{
		{}, {"filter", "--file=logs.parquet"}, {"filter", "--column=content"},
		{"--file=logs.parquet", "--column=content"},
		{"filter", "--file=logs.parquet", "--column=content", "--op=unknown"},
		{"filter", "--file=logs.parquet", "--column=content", "--batch-size=0"},
		{"query", "--file=logs.parquet", "--query=SELECT * FROM logs", "--batch-size=-1"},
		{"filter", "--file=logs.parquet", "--column=content", "unexpected"},
		{"filter", "--file=logs.parquet", "--column=content", "--unknown"},
		{"filter", "--file=", "--column=content"},
		{"filter", "--file=logs.parquet", "--column="},
		{"filter", "--file=logs.parquet", "--column=content", "--value=0", "--string-value="},
		{"filter", "--file=logs.parquet", "--column=content", "--op=regex", "--value=0"},
		{"filter", "--file=logs.parquet", "--column=content", "--op=regex", "--string-value="},
		{"filter", "--file=logs.parquet", "--column=content", "--op=eq", "--pattern="},
		{"filter", "--file=logs.parquet", "--column=content", "--query=SELECT * FROM logs"},
		{"query", "--file=logs.parquet"},
		{"query", "--file=logs.parquet", "--query= "},
	} {
		if _, err := parseCLI(t, args...); err == nil {
			t.Errorf("accepted invalid arguments: %v", args)
		}
	}

	for _, extra := range []string{"--column=content", "--op=ge", "--pattern=", "--value=0", "--string-value="} {
		if _, err := parseCLI(t, "query", "--file=logs.parquet", "--query=SELECT * FROM logs", extra); err == nil {
			t.Errorf("accepted mixed modes with %s", extra)
		}
	}
}

func TestCLIQueryMode(t *testing.T) {
	c, err := parseCLI(t, "query", "--file=../../testdata/bash-example.parquet", "--query=SELECT content FROM logs LIMIT 2", "--debug", "--batch-size=1")
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if _, err := query.Run(t.Context(), &out, c.options(nil)...); err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(out.Bytes(), []byte("\n")); got != 2 {
		t.Fatalf("got %d rows, want 2", got)
	}
}

func TestCLIFilterModes(t *testing.T) {
	for _, flags := range [][]string{
		{"--column=content", "--op=regex", "--pattern=."},
		{"--column=content", "--op=ne", "--string-value="},
		{"--column=flags", "--op=ge", "--value=-1"},
	} {
		args := append([]string{"filter", "--file=../../testdata/bash-example.parquet"}, flags...)
		c, err := parseCLI(t, args...)
		if err != nil {
			t.Fatal(err)
		}

		var out bytes.Buffer
		if _, err := query.Run(t.Context(), &out, c.options(nil)...); err != nil {
			t.Fatal(err)
		}
		if out.Len() == 0 {
			t.Fatalf("no results for %v", flags)
		}
	}
}

func TestCLIHelp(t *testing.T) {
	for _, command := range []string{"filter", "query"} {
		t.Run(command, func(t *testing.T) {
			var c cli
			var out bytes.Buffer
			parser, err := kong.New(&c, kong.Writers(&out, &out), kong.Exit(func(int) {}))
			if err != nil {
				t.Fatal(err)
			}
			args := []string{command, "--file=logs.parquet", "--help"}
			if command == "filter" {
				args = append(args, "--column=content")
			} else {
				args = append(args, "--query=SELECT * FROM logs")
			}
			if _, err := parser.Parse(args); err != nil {
				t.Fatal(err)
			}

			for _, flag := range []string{"--file", "--batch-size", "--explain", "--debug"} {
				if !strings.Contains(out.String(), flag) {
					t.Errorf("%s help missing %s", command, flag)
				}
			}
			if command == "query" && strings.Contains(out.String(), "--column") {
				t.Error("query help includes filter flags")
			}
			if command == "filter" && strings.Contains(out.String(), "--query") {
				t.Error("filter help includes query flags")
			}
		})
	}
}
