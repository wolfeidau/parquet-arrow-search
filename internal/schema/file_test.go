package schema

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpen(t *testing.T) {
	f, err := Open("../../testdata/bash-example.parquet", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	}()
	if f.Schema.NumFields() == 0 {
		t.Fatal("missing schema")
	}
	rr, err := f.Reader.GetRecordReader(t.Context(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !rr.Next() {
		rr.Release()
		t.Fatal("missing batch")
	}
	if rows := rr.RecordBatch().NumRows(); rows < 1 || rows > 2 {
		rr.Release()
		t.Fatalf("batch rows = %d", rows)
	}
	rr.Release()
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("repeated close: %v", err)
	}
}

func TestOpenFailures(t *testing.T) {
	if _, err := Open("unused", 0); err == nil {
		t.Fatal("accepted invalid batch size")
	}
	_, err := Open(filepath.Join(t.TempDir(), "missing.parquet"), 1)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected wrapped missing file error, got %v", err)
	}
	path := filepath.Join(t.TempDir(), "invalid.parquet")
	if err := os.WriteFile(path, []byte("not parquet"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, 1); err == nil || !strings.Contains(err.Error(), "open parquet") {
		t.Fatalf("error = %v", err)
	}
}
