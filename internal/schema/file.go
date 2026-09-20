package schema

import (
	"errors"
	"fmt"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/arrow-go/v18/parquet/file"
	"github.com/apache/arrow-go/v18/parquet/pqarrow"
)

// File owns a Parquet file and provides its Arrow reader and validated flat schema.
// Release any record readers before calling Close.
type File struct {
	Reader  *pqarrow.FileReader
	Schema  *arrow.Schema
	parquet *file.Reader
	path    string
}

// Open loads and validates a local Parquet schema without reading record batches.
func Open(path string, batchSize int64) (_ *File, err error) {
	if batchSize <= 0 {
		return nil, fmt.Errorf("batch size must be positive")
	}

	pf, err := file.OpenParquetFile(path, false)
	if err != nil {
		return nil, fmt.Errorf("open parquet %q: %w", path, err)
	}

	f := &File{parquet: pf, path: path}
	defer func() {
		if err != nil {
			err = errors.Join(err, f.Close())
		}
	}()

	f.Reader, err = pqarrow.NewFileReader(pf, pqarrow.ArrowReadProperties{BatchSize: batchSize}, memory.DefaultAllocator)
	if err != nil {
		return nil, fmt.Errorf("create Arrow reader for %q: %w", path, err)
	}

	f.Schema, err = f.Reader.Schema()
	if err != nil {
		return nil, fmt.Errorf("read schema from %q: %w", path, err)
	}

	if _, err := SubstraitSchema(f.Schema); err != nil {
		return nil, fmt.Errorf("validate schema from %q: %w", path, err)
	}

	return f, nil
}

// Close releases the underlying file once; the Arrow file reader borrows it.
func (f *File) Close() error {
	if f.parquet == nil {
		return nil
	}

	pf := f.parquet
	f.parquet = nil
	if err := pf.Close(); err != nil {
		return fmt.Errorf("close parquet %q: %w", f.path, err)
	}
	return nil
}
