# Parquet Arrow Search

A Go prototype that reads local Parquet files into Apache Arrow batches, builds a
Substrait query plan, and executes an integer comparison or string regex search. Results stream to
stdout as newline-delimited JSON.

CLI errors use structured `slog` logging with `tint` on stderr. Set `NO_COLOR=1`
to disable log colors. Query results and plan JSON remain on stdout.

## Run

The CLI uses Kong. Run `mise exec -- go run ./cmd/parquet-query --help`
for generated help. Long options use double dashes (`--file`, `--debug`).

Use Go 1.27.1 (the version declared in `go.mod`; Go can download it automatically).

```sh
mise exec -- go run ./cmd/parquet-query \
  --file testdata/bash-example.parquet \
  --column content --op regex --pattern '.'
```

The `testdata/` directory contains Bash, Bazel, and Bun log samples. Query your own
file by changing `--file` and `--column`. Supported operators: `eq`, `ne`, `gt`, `ge`,
`lt`, `le`, and `regex`. Use `--batch-size` to change the default 65,536 rows per batch.
Add `--explain` to print the Substrait plan instead of scanning rows.

## Debug logs and performance

Add `--debug` for file/query settings, schema column count, plan setup time, and
per-batch row counts. Logs go to stderr; redirect stdout to save only results:

```sh
mise exec -- go run ./cmd/parquet-query \
  --file testdata/bash-example.parquet \
  --column content --op regex --pattern '(?i)error|failed|panic' \
  --debug > matches.jsonl
```

Every query logs a summary with status, elapsed time, batches, rows scanned,
rows matched, rows successfully written, and rows per second. Elapsed time and
throughput include setup, Parquet decoding, filtering, JSON output, and cleanup;
a slow output consumer affects these numbers. Failed queries report partial
counts with `status=failed`. Explain mode reports plan duration, without scan metrics.
Regex patterns and row contents are not included in debug logs.

## Search log content with regex

```sh
mise exec -- go run ./cmd/parquet-query \
  --file testdata/bash-example.parquet \
  --column content --op regex --pattern '(?i)error|failed|panic'
```

The supplied log files have lowercase column names: `timestamp`, `content`,
`group`, `flags`. Names are case-sensitive. Add `--explain` to inspect the plan.

Regex searches match anywhere in each value. Patterns use Go `regexp` syntax:

| Pattern | Search |
| --- | --- |
| `(?i)error\|failed\|panic` | Any of these words, ignoring case |
| `\bERROR\b` | Uppercase ERROR as a whole word |
| `(?m)^FAIL` | FAIL at the beginning of any line within a value |
| `(?s)error.*timeout` | error followed by timeout, including across newlines |

Quote patterns with single quotes in the shell. Null values never match; an empty
pattern matches all non-null strings. Invalid patterns return an error before any
output. Lookaround and backreferences are unsupported. Matching does not span rows.

The regex is compiled once per query. The plan declares the custom function
`go_regexp_match` from [functions_regex.yaml](internal/query/functions_regex.yaml).
Other engines need an implementation of that extension to execute these plans.
Substrait's standard regex functions specify ICU semantics; this extension
explicitly describes Go semantics instead.

For larger log collections, useful next steps are combined timestamp/group
filters, column projection, and row-group pruning before evaluating regex.

## Design and scope

- [Arrow Go](https://github.com/apache/arrow-go) handles Parquet decoding and Arrow
  memory management.
- [Substrait Go](https://github.com/substrait-io/substrait-go) builds a typed
  `Read -> Filter -> Root` plan and serializes it to protobuf JSON.
- `internal/query` evaluates the generated plan's expression over Arrow arrays.
  This is a small custom executor: Substrait supplies the plan representation.
- The named table in the plan is bound to the local file supplied to the CLI.
  `--explain` reads the file schema but does not scan rows.
- Numeric filter columns must be `int64`; regex filter columns must be UTF-8 strings.
  Output schemas support `int64`, `int32`, UTF-8
  strings, booleans, and `float64`. Other types and duplicate names return errors.
- Null filter values do not match, including for `ne`. All columns are returned.
- Files are scanned in batches; matching rows are encoded individually. There is
  no SQL parser, arbitrary plan import, predicate pushdown, index, projection,
  aggregation, join, or sorting. This is not a general Substrait execution engine.
- Errors can leave partial JSON output. Non-finite floats cannot be JSON-encoded.

## Check

```sh
mise exec -- golangci-lint fmt
mise exec -- go test ./...
mise exec -- golangci-lint run
```
