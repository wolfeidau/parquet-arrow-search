package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/lmittmann/tint"
	"github.com/wolfeidau/parquet-arrow-search/internal/query"
)

type cli struct {
	File      string  `required:"" help:"Local Parquet file."`
	Query     *string `help:"Single-table query: SELECT columns FROM logs [WHERE predicate] [LIMIT n]."`
	Column    *string `help:"Column to filter (required without --query; names are case-sensitive)."`
	Op        *string `enum:"eq,ne,gt,ge,lt,le,regex" help:"Comparison: ${enum} (default: ge)."`
	Pattern   *string `help:"Go regex pattern for --op regex (default: empty)."`
	Value     *int64  `help:"Integer comparison value (default: 0)."`
	BatchSize int64   `default:"65536" help:"Maximum rows per Arrow batch."`
	Explain   bool    `help:"Print Substrait plan JSON instead of rows."`
	Debug     bool    `help:"Enable debug logging on stderr."`
}

func (c *cli) Validate() error {
	if c.BatchSize <= 0 {
		return fmt.Errorf("--batch-size must be positive")
	}
	if c.File == "" {
		return fmt.Errorf("--file must not be empty")
	}

	if c.Query != nil {
		if strings.TrimSpace(*c.Query) == "" {
			return fmt.Errorf("--query must not be empty")
		}
		if c.Column != nil || c.Op != nil || c.Pattern != nil || c.Value != nil {
			return fmt.Errorf("--query cannot be combined with --column, --op, --pattern, or --value")
		}
	} else if c.Column == nil || *c.Column == "" {
		return fmt.Errorf("--column is required without --query")
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
	opts := []query.Option{
		query.WithFile(c.File),
		query.WithBatchSize(c.BatchSize),
		query.WithExplain(c.Explain),
		query.WithLogger(logger),
	}

	if c.Query != nil {
		return append(opts, query.WithQuery(*c.Query))
	}

	opts = append(opts, query.WithColumn(*c.Column))
	if c.Op != nil {
		opts = append(opts, query.WithOperator(*c.Op))
	}
	if c.Pattern != nil {
		opts = append(opts, query.WithPattern(*c.Pattern))
	}
	if c.Value != nil {
		opts = append(opts, query.WithValue(*c.Value))
	}
	return opts
}

func run(opts ...query.Option) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	return query.Run(ctx, os.Stdout, opts...)
}
