package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"

	"github.com/alecthomas/kong"
	"github.com/lmittmann/tint"
	"github.com/wolfeidau/parquet-arrow-search/internal/query"
)

type cli struct {
	File      string `required:"" help:"Local Parquet file."`
	Column    string `required:"" help:"Column to filter (names are case-sensitive)."`
	Op        string `default:"ge" enum:"eq,ne,gt,ge,lt,le,regex" help:"Comparison: ${enum}."`
	Pattern   string `help:"Go regex pattern for --op regex (empty matches all non-null strings)."`
	Value     int64  `default:"0" help:"Integer comparison value."`
	BatchSize int64  `default:"65536" help:"Maximum rows per Arrow batch."`
	Explain   bool   `help:"Print Substrait plan JSON instead of rows."`
	Debug     bool   `help:"Enable debug logging on stderr."`
}

func (c *cli) Validate() error {
	if c.BatchSize <= 0 {
		return fmt.Errorf("--batch-size must be positive")
	}
	if c.File == "" || c.Column == "" {
		return fmt.Errorf("--file and --column must not be empty")
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

	if err := run(args.options(logger)...); err != nil {
		logger.Error("query failed", slog.Any("error", err))
		os.Exit(1)
	}
}

func (c *cli) options(logger *slog.Logger) []query.Option {
	return []query.Option{
		query.WithFile(c.File),
		query.WithColumn(c.Column),
		query.WithOperator(c.Op),
		query.WithPattern(c.Pattern),
		query.WithValue(c.Value),
		query.WithBatchSize(c.BatchSize),
		query.WithExplain(c.Explain),
		query.WithLogger(logger),
	}
}

func run(opts ...query.Option) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	return query.Run(ctx, os.Stdout, opts...)
}
