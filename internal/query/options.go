package query

import "log/slog"

type options struct {
	Query            *string
	legacyFilter     bool
	File, Column, Op string
	Pattern          string
	Value            int64
	BatchSize        int64
	Logger           *slog.Logger
	Explain          bool
}

// Option configures a query. Options are applied in order; the last value wins.
type Option func(*options)

func defaultOptions() options {
	return options{Op: "ge", BatchSize: 65536}
}

// WithFile sets the required local Parquet file.
func WithFile(path string) Option {
	return func(o *options) { o.File = path }
}

// WithColumn sets the case-sensitive filter column, required without WithQuery.
func WithColumn(name string) Option {
	return func(o *options) {
		o.Column = name
		o.legacyFilter = true
	}
}

// WithOperator sets eq, ne, gt, ge, lt, le, or regex. The default is ge.
func WithOperator(op string) Option {
	return func(o *options) {
		o.Op = op
		o.legacyFilter = true
	}
}

// WithPattern sets the pattern used with the regex operator.
// The default empty pattern matches all non-null strings.
func WithPattern(pattern string) Option {
	return func(o *options) {
		o.Pattern = pattern
		o.legacyFilter = true
	}
}

// WithValue sets the integer comparison value. The default is zero.
func WithValue(value int64) Option {
	return func(o *options) {
		o.Value = value
		o.legacyFilter = true
	}
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

// WithQuery selects the single-table SQL subset instead of the legacy filter options.
// It cannot be combined with WithColumn, WithOperator, WithPattern, or WithValue.
func WithQuery(text string) Option {
	return func(o *options) { o.Query = &text }
}
