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

	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/arrow-go/v18/parquet/file"
	"github.com/apache/arrow-go/v18/parquet/pqarrow"
	"github.com/substrait-io/substrait-go/v8/plan"
	"google.golang.org/protobuf/encoding/protojson"
)

const regexOp = "regex"

// Run builds a Substrait plan from the file schema, then explains or executes it.
// Output is newline-delimited JSON; callers may receive partial output on error.
// Defaults: ge comparison against zero, 65,536 rows per batch, no query logs.
// WithFile and either WithQuery or WithColumn are required.
func Run(ctx context.Context, out io.Writer, options ...Option) (err error) {
	opts := defaultOptions()
	for _, option := range options {
		option(&opts)
	}

	started := time.Now()
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	var stats queryStats
	defer func() {
		stats.logSummary(ctx, logger, time.Since(started), opts.Explain, err != nil)
	}()

	startAttrs := []slog.Attr{
		slog.String("file", opts.File),
		slog.Int64("batch_size", opts.BatchSize),
	}
	if opts.Query != nil {
		startAttrs = append(startAttrs, slog.String("mode", "sql"))
	} else {
		startAttrs = append(startAttrs,
			slog.String("column", opts.Column),
			slog.String("operator", opts.Op),
		)
	}
	logger.LogAttrs(ctx, slog.LevelDebug, "starting query", startAttrs...)

	if opts.File == "" || (opts.Query == nil && opts.Column == "") {
		return fmt.Errorf("file and either query or column are required")
	}
	if opts.Query != nil && opts.legacyFilter {
		return fmt.Errorf("query cannot be combined with column, operator, pattern, or value options")
	}
	if opts.BatchSize <= 0 {
		return fmt.Errorf("batch size must be positive")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("start query: %w", err)
	}

	pf, err := file.OpenParquetFile(opts.File, false)
	if err != nil {
		return fmt.Errorf("open parquet %q: %w", opts.File, err)
	}
	defer func() {
		if closeErr := pf.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close parquet %q: %w", opts.File, closeErr))
		}
	}()

	reader, err := pqarrow.NewFileReader(pf, pqarrow.ArrowReadProperties{BatchSize: opts.BatchSize}, memory.DefaultAllocator)
	if err != nil {
		return fmt.Errorf("create Arrow reader for %q: %w", opts.File, err)
	}

	schema, err := reader.Schema()
	if err != nil {
		return fmt.Errorf("read schema from %q: %w", opts.File, err)
	}
	logger.DebugContext(ctx, "read parquet schema", slog.Int("columns", len(schema.Fields())))

	p, err := buildPlan(schema, opts)
	if err != nil {
		return fmt.Errorf("build Substrait plan: %w", err)
	}

	execution, err := compilePlan(p, schema)
	if err != nil {
		return fmt.Errorf("compile query plan: %w", err)
	}

	logger.DebugContext(ctx, "compiled query plan", slog.Duration("setup_elapsed", time.Since(started)))

	if opts.Explain {
		return writePlan(out, p)
	}

	if execution.limit == 0 {
		return nil
	}

	rr, err := reader.GetRecordReader(ctx, nil, nil)
	if err != nil {
		return fmt.Errorf("create record reader for %q: %w", opts.File, err)
	}
	defer rr.Release()

	stats, err = scanRecords(ctx, rr, execution, out, logger)
	if err != nil {
		return fmt.Errorf("scan parquet %q: %w", opts.File, err)
	}

	return nil
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
func scanRecords(ctx context.Context, rr pqarrow.RecordReader, execution *executionPlan, out io.Writer, logger *slog.Logger) (queryStats, error) {
	var stats queryStats
	enc := json.NewEncoder(out)

	for rr.Next() {
		if err := ctx.Err(); err != nil {
			return stats, fmt.Errorf("check scan context: %w", err)
		}

		record := rr.RecordBatch()
		stats.batches++
		logger.DebugContext(ctx, "read Arrow batch",
			slog.Int64("batch", stats.batches),
			slog.Int64("rows", record.NumRows()),
		)

		for row := range int(record.NumRows()) {
			if err := ctx.Err(); err != nil {
				return stats, fmt.Errorf("check scan context: %w", err)
			}

			stats.scanned++
			// Substrait comparisons propagate null; WHERE only keeps true.
			if !execution.matches(record, row) {
				continue
			}

			stats.matched++
			result := make(map[string]any, len(execution.columns))
			for i, column := range execution.columns {
				result[execution.names[i]] = record.Column(column).GetOneForMarshal(row)
			}

			if err := enc.Encode(result); err != nil {
				return stats, fmt.Errorf("write row: %w", err)
			}
			stats.written++

			if execution.limit >= 0 && stats.written >= execution.limit {
				return stats, nil
			}
		}
	}

	if err := rr.Err(); err != nil {
		return stats, fmt.Errorf("read records: %w", err)
	}

	return stats, nil
}
