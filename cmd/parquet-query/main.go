package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"

	"github.com/lmittmann/tint"
	"github.com/wolfeidau/parquet-arrow-search/internal/query"
)

func main() {
	var opts query.Options
	flag.StringVar(&opts.File, "file", "", "local Parquet file (required)")
	flag.StringVar(&opts.Column, "column", "", "column to filter (required; names are case-sensitive)")
	flag.StringVar(&opts.Op, "op", "ge", "comparison: eq, ne, gt, ge, lt, le, regex")
	flag.StringVar(&opts.Pattern, "pattern", "", "Go regex pattern for -op regex (empty matches all non-null strings)")
	flag.Int64Var(&opts.Value, "value", 0, "integer comparison value")
	flag.Int64Var(&opts.BatchSize, "batch-size", 65536, "maximum rows per Arrow batch")
	flag.BoolVar(&opts.Explain, "explain", false, "print Substrait plan JSON instead of rows")
	debug := flag.Bool("debug", false, "enable debug logging on stderr")
	flag.Parse()
	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	logger := slog.New(tint.NewTextHandler(os.Stderr, &tint.Options{
		Level: level, NoColor: os.Getenv("NO_COLOR") != "",
	}))
	opts.Logger = logger
	if opts.File == "" || opts.Column == "" || flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(opts); err != nil {
		logger.Error("query failed", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(opts query.Options) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return query.Run(ctx, opts, os.Stdout)
}
