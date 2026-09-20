package main

import (
	"context"
	"log/slog"

	"github.com/wolfeidau/parquet-arrow-search/internal/query"
)

func logSummary(ctx context.Context, logger *slog.Logger, result query.Result, failed bool) {
	status := "ok"
	if failed {
		status = "failed"
	}

	if result.Explain {
		logger.InfoContext(ctx, "plan finished",
			slog.String("status", status),
			slog.Duration("elapsed", result.Elapsed),
		)
		return
	}

	var rate float64
	if result.Elapsed > 0 {
		rate = float64(result.Scanned) / result.Elapsed.Seconds()
	}

	logger.InfoContext(ctx, "query finished",
		slog.String("status", status),
		slog.Duration("elapsed", result.Elapsed),
		slog.Int64("batches", result.Batches),
		slog.Int64("rows_scanned", result.Scanned),
		slog.Int64("rows_matched", result.Matched),
		slog.Int64("rows_written", result.Written),
		slog.Float64("rows_per_second", rate),
	)
}
