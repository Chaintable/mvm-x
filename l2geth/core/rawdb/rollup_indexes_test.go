package rawdb

import (
	"testing"
)

func TestReadWriteHeadIndex(t *testing.T) {
	indices := []uint64{
		1,
		1 << 2,
		1 << 8,
		1 << 16,
	}

	db := NewMemoryDatabase()
	for _, height := range indices {
		WriteHeadIndex(db, height)
		got := ReadHeadIndex(db)
		if height != *got {
			t.Fatal("Header height mismatch")
		}
	}
}

func TestReadWriteHeadQueueIndex(t *testing.T) {
	indices := []uint64{
		1,
		1 << 4,
		1 << 5,
		1 << 32,
	}

	db := NewMemoryDatabase()
	for _, height := range indices {
		WriteHeadQueueIndex(db, height)
		got := ReadHeadQueueIndex(db)
		if height != *got {
			t.Fatal("Header height mismatch")
		}
	}
}

func TestCopyAndVerifyRollupIndexes(t *testing.T) {
	source := NewMemoryDatabase()
	target := NewMemoryDatabase()

	WriteHeadIndex(source, 101)
	WriteHeadQueueIndex(source, 202)
	WriteHeadVerifiedIndex(source, 303)
	WriteHeadIndexTime(source, 404)
	// Leave LastBatch absent in source and make it stale in target. Copying must
	// delete the target key as well as write the four present source keys.
	WriteHeadBatchIndex(target, 999)

	batch := target.NewBatch()
	if err := CopyRollupIndexes(source, batch); err != nil {
		t.Fatalf("copy rollup indexes: %v", err)
	}
	if err := batch.Write(); err != nil {
		t.Fatalf("write rollup index batch: %v", err)
	}
	if err := VerifyRollupIndexes(source, target); err != nil {
		t.Fatalf("verify rollup indexes: %v", err)
	}

	if got := ReadHeadIndex(target); got == nil || *got != 101 {
		t.Fatalf("unexpected head index: %v", got)
	}
	if got := ReadHeadQueueIndex(target); got == nil || *got != 202 {
		t.Fatalf("unexpected queue index: %v", got)
	}
	if got := ReadHeadVerifiedIndex(target); got == nil || *got != 303 {
		t.Fatalf("unexpected verified index: %v", got)
	}
	if got := ReadHeadIndexTime(target); got == nil || *got != 404 {
		t.Fatalf("unexpected index time: %v", got)
	}
	if got := ReadHeadBatchIndex(target); got != nil {
		t.Fatalf("stale batch index was not deleted: %v", *got)
	}
}

func TestVerifyRollupIndexesDetectsMismatch(t *testing.T) {
	source := NewMemoryDatabase()
	target := NewMemoryDatabase()
	WriteHeadIndex(source, 1)
	WriteHeadIndex(target, 2)

	if err := VerifyRollupIndexes(source, target); err == nil {
		t.Fatal("expected mismatched rollup indexes to fail verification")
	}
}
