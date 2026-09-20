package query

import (
	"log/slog"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/substrait-io/substrait-go/v8/plan"
)

type options struct {
	PlanBuilder func(*arrow.Schema) (*plan.Plan, error)
	File        string
	BatchSize   int64
	Logger      *slog.Logger
	Explain     bool
}

// Option configures a query. Options are applied in order; the last value wins.
type Option func(*options)

func defaultOptions() options {
	return options{BatchSize: 65536}
}

// WithFile sets the required local Parquet file.
func WithFile(path string) Option {
	return func(o *options) { o.File = path }
}

// WithBatchSize sets the positive maximum rows per batch. The default is 65,536.
func WithBatchSize(size int64) Option {
	return func(o *options) { o.BatchSize = size }
}

// WithLogger enables query logs. A nil logger disables them, as by default.
func WithLogger(logger *slog.Logger) Option {
	return func(o *options) { o.Logger = logger }
}

// WithExplain selects plan JSON output instead of scanning. The default is false.
func WithExplain(explain bool) Option {
	return func(o *options) { o.Explain = explain }
}

// WithPlanBuilder supplies the required schema-based plan builder.
// A nil builder is rejected by Run.
func WithPlanBuilder(builder func(*arrow.Schema) (*plan.Plan, error)) Option {
	return func(o *options) { o.PlanBuilder = builder }
}
