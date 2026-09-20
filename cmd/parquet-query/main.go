package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/lmittmann/tint"
	"github.com/wolfeidau/parquet-arrow-search/internal/query"
	"github.com/wolfeidau/parquet-arrow-search/internal/sql"
)

type cli struct {
	File      string `required:"" help:"Local Parquet file."`
	BatchSize int64  `default:"65536" help:"Maximum rows per Arrow batch."`
	Explain   bool   `help:"Print Substrait plan JSON instead of rows."`
	Debug     bool   `help:"Enable debug logging on stderr."`

	Filter filterCommand `cmd:"" help:"Filter one column using comparison or regex flags."`
	Query  queryCommand  `cmd:"" help:"Query selected columns using a small SQL subset."`
}

type filterCommand struct {
	Column      string  `required:"" help:"Column to filter (names are case-sensitive)."`
	Op          string  `default:"ge" enum:"eq,ne,gt,ge,lt,le,regex" help:"Comparison: ${enum}."`
	Pattern     *string `help:"Go regex pattern for --op regex (default: empty)."`
	Value       *int64  `xor:"value" help:"Integer comparison value (default: 0)."`
	StringValue *string `xor:"value" help:"String comparison value; mutually exclusive with --value."`
}

type queryCommand struct {
	Query string `required:"" help:"SELECT columns FROM logs [WHERE predicate] [LIMIT n]."`
}

func (c *cli) Validate() error {
	if c.BatchSize <= 0 {
		return fmt.Errorf("--batch-size must be positive")
	}
	if c.File == "" {
		return fmt.Errorf("--file must not be empty")
	}

	return nil
}

func (c *filterCommand) Validate() error {
	if c.Column == "" {
		return fmt.Errorf("--column must not be empty")
	}
	if c.Op == "regex" && (c.Value != nil || c.StringValue != nil) {
		return fmt.Errorf("--op regex cannot be combined with --value or --string-value")
	}
	if c.Op != "regex" && c.Pattern != nil {
		return fmt.Errorf("--pattern requires --op regex")
	}

	return nil
}

func (c *queryCommand) Validate() error {
	if strings.TrimSpace(c.Query) == "" {
		return fmt.Errorf("--query must not be empty")
	}

	return nil
}

func main() {
	var args cli
	kong.Parse(&args,
		kong.Name("parquet-query"),
		kong.Description("Query a local Parquet file using a Substrait filter. Results go to stdout; logs go to stderr."),
		kong.Writers(os.Stderr, os.Stderr),
	)

	level := slog.LevelInfo
	if args.Debug {
		level = slog.LevelDebug
	}

	logger := slog.New(tint.NewTextHandler(os.Stderr, &tint.Options{
		Level:   level,
		NoColor: os.Getenv("NO_COLOR") != "",
	}))

	if err := run(logger, args.options(logger)...); err != nil {
		logger.Error("query failed", slog.Any("error", err))
		os.Exit(1)
	}
}

func (c *cli) options(logger *slog.Logger) []query.Option {
	opts := []query.Option{
		query.WithFile(c.File),
		query.WithBatchSize(c.BatchSize),
		query.WithExplain(c.Explain),
		query.WithLogger(logger),
	}

	if c.Query.Query != "" {
		return append(opts, query.WithPlanBuilder(sql.Planner(c.Query.Query)))
	}

	opts = append(opts,
		query.WithColumn(c.Filter.Column),
		query.WithOperator(c.Filter.Op),
	)
	if c.Filter.Pattern != nil {
		opts = append(opts, query.WithPattern(*c.Filter.Pattern))
	}
	if c.Filter.Value != nil {
		opts = append(opts, query.WithValue(*c.Filter.Value))
	}
	if c.Filter.StringValue != nil {
		opts = append(opts, query.WithStringValue(*c.Filter.StringValue))
	}
	return opts
}

func run(logger *slog.Logger, opts ...query.Option) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	return execute(ctx, os.Stdout, logger, opts...)
}

// execute keeps presentation at the CLI boundary while preserving partial results.
func execute(ctx context.Context, out io.Writer, logger *slog.Logger, opts ...query.Option) error {
	result, err := query.Run(ctx, out, opts...)
	logSummary(ctx, logger, result, err != nil)
	if err != nil {
		return fmt.Errorf("execute query: %w", err)
	}

	return nil
}
