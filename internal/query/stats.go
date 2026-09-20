package query

import "time"

// Result describes completed work, including partial counts when Run returns an error.
// Elapsed includes setup, execution, output, and resource cleanup.
type Result struct {
	Elapsed time.Duration
	Batches int64 // Arrow batches visited.
	Scanned int64 // Rows evaluated.
	Matched int64 // Matching rows, including a row whose output failed.
	Written int64 // Rows successfully written.
	Explain bool  // Plan-only mode; scan counts are zero.
}
