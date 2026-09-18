# Parquet Arrow Search

Have a big file of logs and only want the lines that mention an error? This small
Go project shows how to find them. Here's how the pieces fit together:

1. Start with a box of logs. Parquet keeps them packed into a compact file.
2. Put a little on the workbench. Arrow lays out a batch of data so the program
   can work through it without unpacking the whole box at once.
3. Write down what to find. Substrait turns your search into an instruction sheet
   that other tools can read if they support the same instructions.
4. Find the useful bits. This program follows the instructions, prints the matching
   rows, and tells you how much it searched and how long it took.

You can try it with the included log files—no database server needed.
[Run your first search](#run), or read on to see why these pieces matter.

## Why Arrow and Substrait?

Searching Parquet logs involves three separate concerns: storing the data,
representing it in memory, and describing the query. This project uses an open
format for each:

| Technology | Role | Why it matters |
| --- | --- | --- |
| Parquet | Stores compressed, typed columns on disk. | Keeps log archives compact while preserving fields such as timestamps, content, and flags. |
| [Apache Arrow](https://github.com/apache/arrow-go) | Represents decoded data as typed columns in memory. | Provides a common layout for batch processing and exchanging data between compatible tools. |
| [Substrait](https://github.com/substrait-io/substrait-go) | Represents queries as typed plans containing operations such as reads and filters. | Makes query intent explicit and inspectable, with a standard representation that compatible engines can exchange. |

### Arrow provides the data representation.

The reader decodes Parquet into Arrow batches, so the query can work through a large file without loading the entire
dataset at once. Values from a column sit together, providing a foundation for
efficient column operations. This prototype evaluates predicates over those
arrays and creates JSON objects only for matching rows.

### Substrait provides the query representation

A search becomes a plan of reads, filters, column selections, and limits, with
explicit field references, types, and functions.
The executor consumes that plan, and `--explain` exposes it as JSON. Keeping the
plan separate from execution creates a path to additional query front ends and
execution backends without inventing a private query format.

Together, they demonstrate a small query pipeline built around reusable data and
plan formats. Execution here is still implemented by this project: Arrow and
Substrait do not automatically provide an optimizer or an index. Plan portability
also depends on engine support; the custom Go regex function requires a matching
implementation in any other engine.

## Run

The CLI uses Kong. Run `mise exec -- go run ./cmd/parquet-query --help`
for generated help. Long options use double dashes (`--file`, `--debug`).

Use Go 1.27.1 (the version declared in `go.mod`; Go can download it automatically).

```sh
mise exec -- go run ./cmd/parquet-query \
  --file testdata/bash-example.parquet \
  --query "SELECT content FROM logs LIMIT 10"
```

The `testdata/` directory contains Bash, Bazel, and Bun log samples. Query your own
file by changing `--file` and `--column`. Supported operators: `eq`, `ne`, `gt`, `ge`,
`lt`, `le`, and `regex`. Use `--batch-size` to change the default 65,536 rows per batch.
Add `--explain` to print the Substrait plan instead of scanning rows.

## Single-table queries

Use `--query` to search the file as a table named `logs`:

```sh
mise exec -- go run ./cmd/parquet-query \
  --file testdata/bun_build_19487_windows-x64-build-cpp.parquet \
  --query "SELECT timestamp, content FROM logs
           WHERE regex(content, '(?i)error|failed|panic')
           LIMIT 100" \
  --debug
```

This is a small SQL subset:

```sql
SELECT * | column, ...
FROM logs
[WHERE predicate]
[LIMIT non-negative-integer]
```

Examples of supported predicates:

```sql
SELECT content FROM logs WHERE flags = 0 LIMIT 20
SELECT timestamp, content FROM logs WHERE content != ''
SELECT content FROM logs WHERE "group" = 'build'
SELECT * FROM logs WHERE content IS NULL
SELECT content FROM logs WHERE regex(content, '\bERROR\b')
```

- `SELECT` accepts `*` or a list of column names. A filter can reference a column
  that is not selected. Duplicate selected names are rejected.
- `WHERE` accepts one comparison (`=`, `!=`, `<>`, `<`, `<=`, `>`, `>=`),
  `regex(column, 'pattern')`, or `IS NULL` / `IS NOT NULL`.
- Comparisons support `int32`, `int64`, and string columns. Literals must match
  the column type; integers are checked for overflow. Null comparisons do not
  match. Use `IS NULL` to find missing values.
- Keywords are case-insensitive. Table and column names are case-sensitive.
  Double-quote column names containing spaces or keywords; double an embedded
  quote, as in `"odd""name"`. String literals use single quotes, with `''` for an
  apostrophe. Backslashes are preserved, so regex escapes need no extra SQL layer.
- `LIMIT` counts matching rows and stops execution early, in file order. A decoded
  batch may contain extra rows that are never evaluated. `LIMIT 0` validates the
  query and produces no rows without reading data batches.
- A trailing semicolon is optional. Compound predicates (`AND`, `OR`, `NOT`),
  aliases, expressions in `SELECT`, joins, aggregation, sorting, and subqueries
  are not supported in this first version. Unsupported syntax returns an error.

Add `--explain` to inspect the Substrait plan. The executor derives the predicate,
selected columns, and limit from that same plan. Query text is parsed with
[Participle](https://github.com/alecthomas/participle), then checked against the
Parquet schema before any rows are scanned.

The existing `--column`, `--op`, `--pattern`, and `--value` flags still work as an
alternative to `--query`. Mixing the two query modes is an error. In Go code,
use `query.WithQuery(text)` alongside `query.WithFile(path)`.

## Debug logs and performance

CLI logs use structured `slog` logging with `tint` on stderr. Set `NO_COLOR=1`
to disable log colors. Query results and plan JSON remain on stdout.

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
Query text, regex patterns, and row contents are not included in debug logs.
With `LIMIT`, counts cover only the rows evaluated before stopping, not the
whole file. Batch counts include batches decoded before that stop.

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
filters and row-group pruning before evaluating regex.

## Design and scope

- [Arrow Go](https://github.com/apache/arrow-go) handles Parquet decoding and Arrow
  memory management.
- [Substrait Go](https://github.com/substrait-io/substrait-go) builds a typed
  `Read -> Filter -> Project -> Fetch -> Root` plan (omitting unneeded operations)
  and serializes it to protobuf JSON.
- `internal/query` evaluates the generated plan's expression over Arrow arrays.
  This is a small custom executor: Substrait supplies the plan representation.
- The named table in the plan is bound to the local file supplied to the CLI.
  `--explain` reads the file schema but does not scan rows.
- SQL comparisons support `int32`, `int64`, and strings. Numeric filters through
  the original CLI flags require `int64`. Regex columns must be UTF-8 strings.
  Output schemas support `int64`, `int32`, UTF-8
  strings, booleans, and `float64`. Other types and duplicate names return errors.
- Null comparisons do not match, including for `ne` and `!=`; explicit null tests
  are available in SQL mode. `SELECT` controls which columns are returned.
- Files are scanned in batches; matching rows are encoded individually. There is
  no arbitrary plan import, predicate pushdown, index, aggregation, join, or
  sorting. Output projection currently still decodes all source columns. This is not a general Substrait execution engine.
- Errors can leave partial JSON output. Non-finite floats cannot be JSON-encoded.

## Check

```sh
mise exec -- golangci-lint fmt
mise exec -- go test ./...
mise exec -- golangci-lint run
```

## License

Copyright 2026 Mark Wolfe. Licensed under the Apache License, Version 2.0.
See [LICENSE](./LICENSE).
