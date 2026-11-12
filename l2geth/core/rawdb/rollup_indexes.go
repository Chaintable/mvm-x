package rawdb

import (
	"bytes"
	"fmt"
	"math/big"

	"github.com/MetisProtocol/mvm/l2geth/ethdb"
	"github.com/MetisProtocol/mvm/l2geth/log"
)

var rollupIndexKeys = []struct {
	name string
	key  []byte
}{
	{"LastIndex", headIndexKey},
	{"LastQueueIndex", headQueueIndexKey},
	{"LastVerifiedIndex", headVerifiedIndexKey},
	{"LastBatch", headBatchKey},
	{"LastIndexTime", headIndexTimeKey},
}

// CopyRollupIndexes copies the rollup synchronization checkpoint into target.
// Missing source keys are deleted from target so the two checkpoints are exact.
func CopyRollupIndexes(source ethdb.KeyValueReader, target ethdb.KeyValueWriter) error {
	for _, entry := range rollupIndexKeys {
		has, err := source.Has(entry.key)
		if err != nil {
			return fmt.Errorf("check source rollup index %s: %w", entry.name, err)
		}
		if !has {
			if err := target.Delete(entry.key); err != nil {
				return fmt.Errorf("delete target rollup index %s: %w", entry.name, err)
			}
			continue
		}
		value, err := source.Get(entry.key)
		if err != nil {
			return fmt.Errorf("read source rollup index %s: %w", entry.name, err)
		}
		if err := target.Put(entry.key, value); err != nil {
			return fmt.Errorf("write target rollup index %s: %w", entry.name, err)
		}
	}
	return nil
}

// VerifyRollupIndexes checks that source and target have identical rollup
// synchronization checkpoints.
func VerifyRollupIndexes(source ethdb.KeyValueReader, target ethdb.KeyValueReader) error {
	for _, entry := range rollupIndexKeys {
		sourceHas, err := source.Has(entry.key)
		if err != nil {
			return fmt.Errorf("check source rollup index %s: %w", entry.name, err)
		}
		targetHas, err := target.Has(entry.key)
		if err != nil {
			return fmt.Errorf("check target rollup index %s: %w", entry.name, err)
		}
		if sourceHas != targetHas {
			return fmt.Errorf("rollup index %s presence mismatch", entry.name)
		}
		if !sourceHas {
			continue
		}
		sourceValue, err := source.Get(entry.key)
		if err != nil {
			return fmt.Errorf("read source rollup index %s: %w", entry.name, err)
		}
		targetValue, err := target.Get(entry.key)
		if err != nil {
			return fmt.Errorf("read target rollup index %s: %w", entry.name, err)
		}
		if !bytes.Equal(sourceValue, targetValue) {
			return fmt.Errorf("rollup index %s value mismatch", entry.name)
		}
	}
	return nil
}

// ReadHeadIndex will read the known tip of the CTC
func ReadHeadIndex(db ethdb.KeyValueReader) *uint64 {
	data, _ := db.Get(headIndexKey)
	if len(data) == 0 {
		return nil
	}
	ret := new(big.Int).SetBytes(data).Uint64()
	return &ret
}

// WriteHeadIndex will write the known tip of the CTC
func WriteHeadIndex(db ethdb.KeyValueWriter, index uint64) {
	value := new(big.Int).SetUint64(index).Bytes()
	if index == 0 {
		value = []byte{0}
	}
	if err := db.Put(headIndexKey, value); err != nil {
		log.Crit("Failed to store index", "err", err)
	}
}

// ReadHeadIndexTime will read the known tip of the CTC
func ReadHeadIndexTime(db ethdb.KeyValueReader) *uint64 {
	data, _ := db.Get(headIndexTimeKey)
	if len(data) == 0 {
		return nil
	}
	ret := new(big.Int).SetBytes(data).Uint64()
	return &ret
}

// WriteHeadIndexTime will write the known tip of the CTC
func WriteHeadIndexTime(db ethdb.KeyValueWriter, indexTime int64) {
	value := new(big.Int).SetInt64(indexTime).Bytes()
	if indexTime == 0 {
		value = []byte{0}
	}
	if err := db.Put(headIndexTimeKey, value); err != nil {
		log.Crit("Failed to store index", "err", err)
	}
}

// ReadHeadQueueIndex will read the known tip of the queue
func ReadHeadQueueIndex(db ethdb.KeyValueReader) *uint64 {
	data, _ := db.Get(headQueueIndexKey)
	if len(data) == 0 {
		return nil
	}
	ret := new(big.Int).SetBytes(data).Uint64()
	return &ret
}

// WriteHeadQueueIndex will write the known tip of the queue
func WriteHeadQueueIndex(db ethdb.KeyValueWriter, index uint64) {
	value := new(big.Int).SetUint64(index).Bytes()
	if index == 0 {
		value = []byte{0}
	}
	if err := db.Put(headQueueIndexKey, value); err != nil {
		log.Crit("Failed to store queue index", "err", err)
	}
}

// ReadHeadVerifiedIndex will read the known tip of the batched transactions
func ReadHeadVerifiedIndex(db ethdb.KeyValueReader) *uint64 {
	data, _ := db.Get(headVerifiedIndexKey)
	if len(data) == 0 {
		return nil
	}
	ret := new(big.Int).SetBytes(data).Uint64()
	return &ret
}

// WriteHeadVerifiedIndex will write the known tip of the batched transactions
func WriteHeadVerifiedIndex(db ethdb.KeyValueWriter, index uint64) {
	value := new(big.Int).SetUint64(index).Bytes()
	if index == 0 {
		value = []byte{0}
	}
	if err := db.Put(headVerifiedIndexKey, value); err != nil {
		log.Crit("Failed to store verifier index", "err", err)
	}
}

// ReadHeadBatchIndex will read the known tip of the processed batches
func ReadHeadBatchIndex(db ethdb.KeyValueReader) *uint64 {
	data, _ := db.Get(headBatchKey)
	if len(data) == 0 {
		return nil
	}
	ret := new(big.Int).SetBytes(data).Uint64()
	return &ret
}

// WriteHeadBatchIndex will write the known tip of the processed batches
func WriteHeadBatchIndex(db ethdb.KeyValueWriter, index uint64) {
	value := new(big.Int).SetUint64(index).Bytes()
	if index == 0 {
		value = []byte{0}
	}
	if err := db.Put(headBatchKey, value); err != nil {
		log.Crit("Failed to store head batch index", "err", err)
	}
}
