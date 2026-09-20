// Package query implements a small Substrait executor for local Parquet files.
package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/apache/arrow-go/v18/parquet/pqarrow"
	"github.com/substrait-io/substrait-go/v8/plan"
	"github.com/wolfeidau/parquet-arrow-search/internal/schema"
	"google.golang.org/protobuf/encoding/protojson"
)

// Run builds a Substrait plan from the file schema, then explains or executes it.
// Output is newline-delimited JSON; callers may receive partial output on error.
// The returned result includes partial counts and elapsed time through cleanup.
// Defaults: 65,536 rows per batch, no query logs.
// WithFile and WithPlanBuilder are required.
func Run(ctx context.Context, out io.Writer, options ...Option) (result Result, err error) {
	opts := defaultOptions()
	for _, option := range options {
		option(&opts)
	}

	started := time.Now()
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	defer func() {
		result.Elapsed = time.Since(started)
		result.Explain = opts.Explain
	}()

	logger.DebugContext(ctx, "starting query",
		slog.String("file", opts.File),
		slog.Int64("batch_size", opts.BatchSize),
	)

	if opts.File == "" || opts.PlanBuilder == nil {
		return result, fmt.Errorf("file and plan builder are required")
	}
	if opts.BatchSize <= 0 {
		return result, fmt.Errorf("batch size must be positive")
	}
	if err := ctx.Err(); err != nil {
		return result, fmt.Errorf("start query: %w", err)
	}

	source, err := schema.Open(opts.File, opts.BatchSize)
	if err != nil {
		return result, fmt.Errorf("load parquet: %w", err)
	}
	defer func() {
		if closeErr := source.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()
	reader := source.Reader
	fileSchema := source.Schema
	logger.DebugContext(ctx, "read parquet schema", slog.Int("columns", len(fileSchema.Fields())))

	p, err := opts.PlanBuilder(fileSchema)
	if err != nil {
		return result, fmt.Errorf("build Substrait plan: %w", err)
	}

	if p == nil {
		return result, fmt.Errorf("plan builder returned a nil plan")
	}

	execution, err := compilePlan(p, fileSchema)
	if err != nil {
		return result, fmt.Errorf("compile query plan: %w", err)
	}

	logger.DebugContext(ctx, "compiled query plan", slog.Duration("setup_elapsed", time.Since(started)))

	if opts.Explain {
		err = writePlan(out, p)
		return result, err
	}

	if execution.limit == 0 {
		return result, nil
	}

	rr, err := reader.GetRecordReader(ctx, nil, nil)
	if err != nil {
		return result, fmt.Errorf("create record reader for %q: %w", opts.File, err)
	}
	defer rr.Release()

	result, err = scanRecords(ctx, rr, execution, out, logger)
	if err != nil {
		return result, fmt.Errorf("scan parquet %q: %w", opts.File, err)
	}

	return result, nil
}

// writePlan writes an inspectable protobuf JSON representation of the plan.
func writePlan(out io.Writer, p *plan.Plan) error {
	pb, err := p.ToProto()
	if err != nil {
		return fmt.Errorf("convert Substrait plan to protobuf: %w", err)
	}

	data, err := (protojson.MarshalOptions{Indent: "  ", UseProtoNames: true}).Marshal(pb)
	if err != nil {
		return fmt.Errorf("marshal Substrait plan JSON: %w", err)
	}

	if _, err := fmt.Fprintln(out, string(data)); err != nil {
		return fmt.Errorf("write Substrait plan JSON: %w", err)
	}

	return nil
}

// scanRecords borrows rr; its caller retains ownership and releases it.
func scanRecords(ctx context.Context, rr pqarrow.RecordReader, execution *executionPlan, out io.Writer, logger *slog.Logger) (Result, error) {
	var stats Result
	enc := json.NewEncoder(out)

	for rr.Next() {
		if err := ctx.Err(); err != nil {
			return stats, fmt.Errorf("check scan context: %w", err)
		}

		record := rr.RecordBatch()
		stats.Batches++
		logger.DebugContext(ctx, "read Arrow batch",
			slog.Int64("batch", stats.Batches),
			slog.Int64("rows", record.NumRows()),
		)

		for row := range int(record.NumRows()) {
			if err := ctx.Err(); err != nil {
				return stats, fmt.Errorf("check scan context: %w", err)
			}

			stats.Scanned++
			// Substrait comparisons propagate null; WHERE only keeps true.
			if !execution.matches(record, row) {
				continue
			}

			stats.Matched++
			result := make(map[string]any, len(execution.columns))
			for i, column := range execution.columns {
				result[execution.names[i]] = record.Column(column).GetOneForMarshal(row)
			}

			if err := enc.Encode(result); err != nil {
				return stats, fmt.Errorf("write row: %w", err)
			}
			stats.Written++

			if execution.limit >= 0 && stats.Written >= execution.limit {
				return stats, nil
			}
		}
	}

	if err := rr.Err(); err != nil {
		return stats, fmt.Errorf("read records: %w", err)
	}

	return stats, nil
}
