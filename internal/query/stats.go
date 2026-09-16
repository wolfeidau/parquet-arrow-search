package query

import (
	"context"
	"log/slog"
	"time"
)

type queryStats struct {
	batches, scanned, matched, written int64
}

func (s queryStats) logSummary(ctx context.Context, logger *slog.Logger, elapsed time.Duration, explain, failed bool) {
	status := "ok"
	if failed {
		status = "failed"
	}

	if explain {
		logger.InfoContext(ctx, "plan finished",
			slog.String("status", status),
			slog.Duration("elapsed", elapsed),
		)
		return
	}

	var rate float64
	if elapsed > 0 {
		rate = float64(s.scanned) / elapsed.Seconds()
	}

	logger.InfoContext(ctx, "query finished",
		slog.String("status", status),
		slog.Duration("elapsed", elapsed),
		slog.Int64("batches", s.batches),
		slog.Int64("rows_scanned", s.scanned),
		slog.Int64("rows_matched", s.matched),
		slog.Int64("rows_written", s.written),
		slog.Float64("rows_per_second", rate),
	)
}
