// Copyright 2015 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/MetisProtocol/mvm/l2geth/common/hexutil"

	"gopkg.in/urfave/cli.v1"

	"github.com/MetisProtocol/mvm/l2geth/cmd/utils"
	"github.com/MetisProtocol/mvm/l2geth/common"
	"github.com/MetisProtocol/mvm/l2geth/console"
	"github.com/MetisProtocol/mvm/l2geth/core"
	"github.com/MetisProtocol/mvm/l2geth/core/rawdb"
	"github.com/MetisProtocol/mvm/l2geth/core/state"
	"github.com/MetisProtocol/mvm/l2geth/core/types"
	"github.com/MetisProtocol/mvm/l2geth/eth/downloader"
	"github.com/MetisProtocol/mvm/l2geth/ethdb"
	"github.com/MetisProtocol/mvm/l2geth/event"
	"github.com/MetisProtocol/mvm/l2geth/log"
	"github.com/MetisProtocol/mvm/l2geth/trie"
)

var (
	copydbFullTailFlag = cli.Uint64Flag{
		Name:  "copydb.full-tail",
		Usage: "Number of recent blocks to execute fully after the pruned state pivot",
		Value: 256,
	}
	initCommand = cli.Command{
		Action:    utils.MigrateFlags(initGenesis),
		Name:      "init",
		Usage:     "Bootstrap and initialize a new genesis block",
		ArgsUsage: "<genesisPathOrUrl> (<genesisHash>)",
		Flags: []cli.Flag{
			utils.DataDirFlag,
			utils.RollupGenesisTimeoutSecondsFlag,
		},
		Category: "BLOCKCHAIN COMMANDS",
		Description: `
The init command initializes a new genesis block and definition for the network.
This is a destructive action and changes the network in which you will be
participating.

It expects either a path or an HTTP URL to the genesis file as an argument. If an
HTTP URL is specified for the genesis file, then a hex-encoded SHA256 hash of the
genesis file must be included as a second argument. The hash provided on the CLI
will be checked against the hash of the genesis file downloaded from the URL.`,
	}
	dumpChainCfgCommand = cli.Command{
		Action: utils.MigrateFlags(dumpChainCfg),
		Name:   "dump-chain-cfg",
		Usage:  "Dumps the current chain config to standard out.",
		Flags: []cli.Flag{
			utils.DataDirFlag,
		},
		Category: "BLOCKCHAIN COMMANDS",
		Description: `
This command dumps the currently configured chain state to standard output. It
will fail if there is no genesis block configured.`,
	}
	importCommand = cli.Command{
		Action:    utils.MigrateFlags(importChain),
		Name:      "import",
		Usage:     "Import a blockchain file",
		ArgsUsage: "<filename> (<filename 2> ... <filename N>) ",
		Flags: []cli.Flag{
			utils.DataDirFlag,
			utils.CacheFlag,
			utils.SyncModeFlag,
			utils.GCModeFlag,
			utils.CacheDatabaseFlag,
			utils.CacheGCFlag,
		},
		Category: "BLOCKCHAIN COMMANDS",
		Description: `
The import command imports blocks from an RLP-encoded form. The form can be one file
with several RLP-encoded blocks, or several files can be used.

If only one file is used, import error will result in failure. If several files are used,
processing will proceed even if an individual RLP-file import failure occurs.`,
	}
	exportCommand = cli.Command{
		Action:    utils.MigrateFlags(exportChain),
		Name:      "export",
		Usage:     "Export blockchain into file",
		ArgsUsage: "<filename> [<blockNumFirst> <blockNumLast>]",
		Flags: []cli.Flag{
			utils.DataDirFlag,
			utils.CacheFlag,
			utils.SyncModeFlag,
		},
		Category: "BLOCKCHAIN COMMANDS",
		Description: `
Requires a first argument of the file to write to.
Optional second and third arguments control the first and
last block to write. In this mode, the file will be appended
if already existing. If the file ends with .gz, the output will
be gzipped.`,
	}
	importPreimagesCommand = cli.Command{
		Action:    utils.MigrateFlags(importPreimages),
		Name:      "import-preimages",
		Usage:     "Import the preimage database from an RLP stream",
		ArgsUsage: "<datafile>",
		Flags: []cli.Flag{
			utils.DataDirFlag,
			utils.CacheFlag,
			utils.SyncModeFlag,
		},
		Category: "BLOCKCHAIN COMMANDS",
		Description: `
	The import-preimages command imports hash preimages from an RLP encoded stream.`,
	}
	exportPreimagesCommand = cli.Command{
		Action:    utils.MigrateFlags(exportPreimages),
		Name:      "export-preimages",
		Usage:     "Export the preimage database into an RLP stream",
		ArgsUsage: "<dumpfile>",
		Flags: []cli.Flag{
			utils.DataDirFlag,
			utils.CacheFlag,
			utils.SyncModeFlag,
		},
		Category: "BLOCKCHAIN COMMANDS",
		Description: `
The export-preimages command export hash preimages to an RLP encoded stream`,
	}
	copydbCommand = cli.Command{
		Action:    utils.MigrateFlags(copyDb),
		Name:      "copydb",
		Usage:     "Create a pruned full-mode chain from an offline chaindata folder",
		ArgsUsage: "<sourceChaindataDir> <sourceAncientDir>",
		Flags: []cli.Flag{
			utils.DataDirFlag,
			utils.AncientFlag,
			utils.CacheFlag,
			utils.SyncModeFlag,
			utils.GCModeFlag,
			utils.FakePoWFlag,
			utils.TestnetFlag,
			utils.RinkebyFlag,
			copydbFullTailFlag,
		},
		Category: "BLOCKCHAIN COMMANDS",
		Description: `
The target datadir must first be initialized with the same genesis as the source.
The source must be offline and the target must contain only the initialized genesis.`,
	}
	removedbCommand = cli.Command{
		Action:    utils.MigrateFlags(removeDB),
		Name:      "removedb",
		Usage:     "Remove blockchain and state databases",
		ArgsUsage: " ",
		Flags: []cli.Flag{
			utils.DataDirFlag,
		},
		Category: "BLOCKCHAIN COMMANDS",
		Description: `
Remove blockchain and state databases`,
	}
	dumpCommand = cli.Command{
		Action:    utils.MigrateFlags(dump),
		Name:      "dump",
		Usage:     "Dump a specific block from storage",
		ArgsUsage: "[<blockHash> | <blockNum>]...",
		Flags: []cli.Flag{
			utils.DataDirFlag,
			utils.CacheFlag,
			utils.SyncModeFlag,
			utils.IterativeOutputFlag,
			utils.ExcludeCodeFlag,
			utils.ExcludeStorageFlag,
			utils.IncludeIncompletesFlag,
		},
		Category: "BLOCKCHAIN COMMANDS",
		Description: `
The arguments are interpreted as block numbers or hashes.
Use "ethereum dump 0" to dump the genesis block.`,
	}
	inspectCommand = cli.Command{
		Action:    utils.MigrateFlags(inspect),
		Name:      "inspect",
		Usage:     "Inspect the storage size for each type of data in the database",
		ArgsUsage: " ",
		Flags: []cli.Flag{
			utils.DataDirFlag,
			utils.AncientFlag,
			utils.CacheFlag,
			utils.TestnetFlag,
			utils.RinkebyFlag,
			utils.GoerliFlag,
			utils.SyncModeFlag,
		},
		Category: "BLOCKCHAIN COMMANDS",
	}
)

// initGenesis will initialise the given JSON format genesis file and writes it as
// the zero'd block (i.e. genesis) or will fail hard if it can't succeed.
func initGenesis(ctx *cli.Context) error {
	// Make sure we have a valid genesis JSON
	genesisPathOrURL := ctx.Args().First()
	if len(genesisPathOrURL) == 0 {
		utils.Fatalf("Must supply path or URL to genesis JSON file")
	}

	var file io.ReadCloser
	if matched, _ := regexp.MatchString("^http(s)?://", genesisPathOrURL); matched {
		genesisHashStr := ctx.Args().Get(1)
		if genesisHashStr == "" {
			utils.Fatalf("Must specify a genesis hash argument if the genesis path argument is an URL.")
		}

		genesisHashData, err := hexutil.Decode(genesisHashStr)
		if err != nil {
			utils.Fatalf("Error decoding genesis hash: %v", err)
		}

		log.Info("Fetching genesis file", "url", genesisPathOrURL)

		genesisData, err := fetchGenesis(genesisPathOrURL, time.Duration(ctx.GlobalInt(utils.RollupGenesisTimeoutSecondsFlag.Name)))
		if err != nil {
			utils.Fatalf("Failed to fetch genesis file: %v", err)
		}

		hash := sha256.New()
		hash.Write(genesisData)
		actualHash := hash.Sum(nil)
		if !bytes.Equal(actualHash, genesisHashData) {
			utils.Fatalf(
				"Genesis hashes do not match. Need: %s, got: %s",
				genesisHashStr,
				hexutil.Encode(actualHash),
			)
		}

		file = ioutil.NopCloser(bytes.NewReader(genesisData))
	} else {
		var err error
		file, err = os.Open(genesisPathOrURL)
		if err != nil {
			utils.Fatalf("Failed to read genesis file: %v", err)
		}
		defer file.Close()
	}

	genesis := new(core.Genesis)
	if err := json.NewDecoder(file).Decode(genesis); err != nil {
		utils.Fatalf("invalid genesis file: %v", err)
	}
	// Open an initialise both full and light databases
	stack := makeFullNode(ctx)
	defer stack.Close()

	for _, name := range []string{"chaindata", "lightchaindata"} {
		chaindb, err := stack.OpenDatabase(name, 0, 0, "")
		if err != nil {
			utils.Fatalf("Failed to open database: %v", err)
		}
		_, hash, err := core.SetupGenesisBlock(chaindb, genesis)
		if err != nil {
			utils.Fatalf("Failed to write genesis block: %v", err)
		}
		chaindb.Close()
		log.Info("Successfully wrote genesis state", "database", name, "hash", hash)
	}
	return nil
}

// dumpChainCfg dumps chain config to standard output.
func dumpChainCfg(ctx *cli.Context) error {
	stack := makeFullNode(ctx)
	defer stack.Close()

	db, err := stack.OpenDatabase("chaindata", 0, 0, "")
	if err != nil {
		utils.Fatalf("Failed to open database: %v", err)
	}

	stored := rawdb.ReadCanonicalHash(db, 0)
	var zeroHash common.Hash
	if stored == zeroHash {
		utils.Fatalf("No genesis block configured.")
	}
	chainCfg := rawdb.ReadChainConfig(db, stored)
	out, err := json.MarshalIndent(chainCfg, "", "  ")
	if err != nil {
		utils.Fatalf("Failed to marshal chain config: %v", out)
	}
	fmt.Println(string(out))
	return nil
}

func importChain(ctx *cli.Context) error {
	if len(ctx.Args()) < 1 {
		utils.Fatalf("This command requires an argument.")
	}
	stack := makeFullNode(ctx)
	defer stack.Close()

	chain, db := utils.MakeChain(ctx, stack)
	defer db.Close()

	// Start periodically gathering memory profiles
	var peakMemAlloc, peakMemSys uint64
	go func() {
		stats := new(runtime.MemStats)
		for {
			runtime.ReadMemStats(stats)
			if atomic.LoadUint64(&peakMemAlloc) < stats.Alloc {
				atomic.StoreUint64(&peakMemAlloc, stats.Alloc)
			}
			if atomic.LoadUint64(&peakMemSys) < stats.Sys {
				atomic.StoreUint64(&peakMemSys, stats.Sys)
			}
			time.Sleep(5 * time.Second)
		}
	}()
	// Import the chain
	start := time.Now()

	if len(ctx.Args()) == 1 {
		if err := utils.ImportChain(chain, ctx.Args().First()); err != nil {
			log.Error("Import error", "err", err)
		}
	} else {
		for _, arg := range ctx.Args() {
			if err := utils.ImportChain(chain, arg); err != nil {
				log.Error("Import error", "file", arg, "err", err)
			}
		}
	}
	chain.Stop()
	fmt.Printf("Import done in %v.\n\n", time.Since(start))

	// Output pre-compaction stats mostly to see the import trashing
	stats, err := db.Stat("leveldb.stats")
	if err != nil {
		utils.Fatalf("Failed to read database stats: %v", err)
	}
	fmt.Println(stats)

	ioStats, err := db.Stat("leveldb.iostats")
	if err != nil {
		utils.Fatalf("Failed to read database iostats: %v", err)
	}
	fmt.Println(ioStats)

	// Print the memory statistics used by the importing
	mem := new(runtime.MemStats)
	runtime.ReadMemStats(mem)

	fmt.Printf("Object memory: %.3f MB current, %.3f MB peak\n", float64(mem.Alloc)/1024/1024, float64(atomic.LoadUint64(&peakMemAlloc))/1024/1024)
	fmt.Printf("System memory: %.3f MB current, %.3f MB peak\n", float64(mem.Sys)/1024/1024, float64(atomic.LoadUint64(&peakMemSys))/1024/1024)
	fmt.Printf("Allocations:   %.3f million\n", float64(mem.Mallocs)/1000000)
	fmt.Printf("GC pause:      %v\n\n", time.Duration(mem.PauseTotalNs))

	if ctx.GlobalBool(utils.NoCompactionFlag.Name) {
		return nil
	}

	// Compact the entire database to more accurately measure disk io and print the stats
	start = time.Now()
	fmt.Println("Compacting entire database...")
	if err = db.Compact(nil, nil); err != nil {
		utils.Fatalf("Compaction failed: %v", err)
	}
	fmt.Printf("Compaction done in %v.\n\n", time.Since(start))

	stats, err = db.Stat("leveldb.stats")
	if err != nil {
		utils.Fatalf("Failed to read database stats: %v", err)
	}
	fmt.Println(stats)

	ioStats, err = db.Stat("leveldb.iostats")
	if err != nil {
		utils.Fatalf("Failed to read database iostats: %v", err)
	}
	fmt.Println(ioStats)
	return nil
}

func exportChain(ctx *cli.Context) error {
	if len(ctx.Args()) < 1 {
		utils.Fatalf("This command requires an argument.")
	}
	stack := makeFullNode(ctx)
	defer stack.Close()

	chain, _ := utils.MakeChain(ctx, stack)
	start := time.Now()

	var err error
	fp := ctx.Args().First()
	if len(ctx.Args()) < 3 {
		err = utils.ExportChain(chain, fp)
	} else {
		// This can be improved to allow for numbers larger than 9223372036854775807
		first, ferr := strconv.ParseInt(ctx.Args().Get(1), 10, 64)
		last, lerr := strconv.ParseInt(ctx.Args().Get(2), 10, 64)
		if ferr != nil || lerr != nil {
			utils.Fatalf("Export error in parsing parameters: block number not an integer\n")
		}
		if first < 0 || last < 0 {
			utils.Fatalf("Export error: block number must be greater than 0\n")
		}
		err = utils.ExportAppendChain(chain, fp, uint64(first), uint64(last))
	}

	if err != nil {
		utils.Fatalf("Export error: %v\n", err)
	}
	fmt.Printf("Export done in %v\n", time.Since(start))
	return nil
}

// importPreimages imports preimage data from the specified file.
func importPreimages(ctx *cli.Context) error {
	if len(ctx.Args()) < 1 {
		utils.Fatalf("This command requires an argument.")
	}
	stack := makeFullNode(ctx)
	defer stack.Close()

	db := utils.MakeChainDatabase(ctx, stack)
	start := time.Now()

	if err := utils.ImportPreimages(db, ctx.Args().First()); err != nil {
		utils.Fatalf("Import error: %v\n", err)
	}
	fmt.Printf("Import done in %v\n", time.Since(start))
	return nil
}

// exportPreimages dumps the preimage data to specified json file in streaming way.
func exportPreimages(ctx *cli.Context) error {
	if len(ctx.Args()) < 1 {
		utils.Fatalf("This command requires an argument.")
	}
	stack := makeFullNode(ctx)
	defer stack.Close()

	db := utils.MakeChainDatabase(ctx, stack)
	start := time.Now()

	if err := utils.ExportPreimages(db, ctx.Args().First()); err != nil {
		utils.Fatalf("Export error: %v\n", err)
	}
	fmt.Printf("Export done in %v\n", time.Since(start))
	return nil
}

func copyDb(ctx *cli.Context) error {
	if len(ctx.Args()) != 2 {
		return fmt.Errorf("copydb requires source chaindata and source ancient directory paths")
	}
	syncMode := *utils.GlobalTextMarshaler(ctx, utils.SyncModeFlag.Name).(*downloader.SyncMode)
	if syncMode != downloader.FastSync {
		return fmt.Errorf("copydb requires --%s=fast", utils.SyncModeFlag.Name)
	}
	if gcmode := ctx.GlobalString(utils.GCModeFlag.Name); gcmode != "full" {
		return fmt.Errorf("copydb requires --%s=full, got %q", utils.GCModeFlag.Name, gcmode)
	}
	fullTail := ctx.Uint64(copydbFullTailFlag.Name)
	if fullTail < core.TriesInMemory {
		return fmt.Errorf("--%s must be at least %d", copydbFullTailFlag.Name, core.TriesInMemory)
	}

	// Build the target paths before opening either database and ensure the source
	// and target cannot resolve to overlapping directories.
	stack := makeFullNode(ctx)
	defer stack.Close()
	targetChaindata := stack.ResolvePath("chaindata")
	targetAncient := ctx.GlobalString(utils.AncientFlag.Name)
	switch {
	case targetAncient == "":
		targetAncient = filepath.Join(targetChaindata, "ancient")
	case !filepath.IsAbs(targetAncient):
		targetAncient = stack.ResolvePath(targetAncient)
	}
	if err := validateCopyDBPaths(ctx.Args().First(), ctx.Args().Get(1), targetChaindata, targetAncient); err != nil {
		return err
	}

	// Open the offline source and capture the exact cutover head.
	sourceDb, err := rawdb.NewLevelDBDatabaseWithFreezer(ctx.Args().First(), ctx.GlobalInt(utils.CacheFlag.Name)/2, 256, ctx.Args().Get(1), "")
	if err != nil {
		return fmt.Errorf("open source database: %w", err)
	}
	defer sourceDb.Close()
	sourceHead, err := readCopyDBHead(sourceDb)
	if err != nil {
		return fmt.Errorf("read source head: %w", err)
	}

	// Probe the target before MakeChain can initialize a default Ethereum genesis.
	// A valid target must have been initialized explicitly with the Metis genesis.
	targetProbe := utils.MakeChainDatabase(ctx, stack)
	if err := validateCopyDBTarget(sourceDb, targetProbe); err != nil {
		targetProbe.Close()
		return err
	}
	if err := targetProbe.Close(); err != nil {
		return fmt.Errorf("close target database after preflight: %w", err)
	}

	chain, chainDb := utils.MakeChain(ctx, stack)
	defer chainDb.Close()
	defer chain.Stop()

	// Create a source header chain used by the local header and state peers.
	hc, err := core.NewHeaderChain(sourceDb, chain.Config(), chain.Engine(), func() bool { return false })
	if err != nil {
		return fmt.Errorf("open source header chain: %w", err)
	}
	currentHeader := hc.CurrentHeader()
	if currentHeader.Hash() != sourceHead.hash || currentHeader.Number.Uint64() != sourceHead.number {
		return fmt.Errorf("source head changed before synchronization")
	}

	start := time.Now()
	if err := copyPrunedChain(sourceDb, chainDb, chain, hc, sourceHead, fullTail, uint64(ctx.GlobalInt(utils.CacheFlag.Name)/2)); err != nil {
		return err
	}
	fmt.Printf("Database copy done in %v\n", time.Since(start))

	// The source must remain fixed for the entire migration, and the target must
	// finish at exactly the same canonical head and state root.
	finalSourceHead, err := readCopyDBHead(sourceDb)
	if err != nil {
		return fmt.Errorf("re-read source head: %w", err)
	}
	if finalSourceHead != sourceHead {
		return fmt.Errorf("source head changed during copydb")
	}
	if err := verifyCopyDBHead(sourceHead, chain.CurrentBlock()); err != nil {
		return err
	}
	if _, err := chain.StateAt(sourceHead.root); err != nil {
		return fmt.Errorf("open copied head state: %w", err)
	}

	// Carry the Metis rollup checkpoint forward so the writer resumes after the
	// cutover head instead of replaying historical L2 input.
	batch := chainDb.NewBatch()
	if err := rawdb.CopyRollupIndexes(sourceDb, batch); err != nil {
		return fmt.Errorf("copy rollup indexes: %w", err)
	}
	if err := batch.Write(); err != nil {
		return fmt.Errorf("write rollup indexes: %w", err)
	}
	if err := rawdb.VerifyRollupIndexes(sourceDb, chainDb); err != nil {
		return fmt.Errorf("verify rollup indexes: %w", err)
	}

	// Stop the blockchain before syncing and compacting so the recent trie tail is
	// durable, then align the fast-head marker with the fully executed head.
	chain.Stop()
	rawdb.WriteHeadFastBlockHash(chainDb, sourceHead.hash)
	if err := chainDb.Sync(); err != nil {
		return fmt.Errorf("sync target database: %w", err)
	}
	durableHead, err := readCopyDBHead(chainDb)
	if err != nil {
		return fmt.Errorf("read durable target head: %w", err)
	}
	if durableHead != sourceHead {
		return fmt.Errorf("durable target head no longer matches source head")
	}
	if _, err := state.New(sourceHead.root, state.NewDatabaseWithCache(chainDb, 0)); err != nil {
		return fmt.Errorf("reopen copied head state from disk: %w", err)
	}

	// Compact only the target LevelDB to reclaim fast-sync write amplification.
	start = time.Now()
	fmt.Println("Compacting target database...")
	if err = chainDb.Compact(nil, nil); err != nil {
		return fmt.Errorf("compact target database: %w", err)
	}
	if err := chainDb.Sync(); err != nil {
		return fmt.Errorf("sync compacted target database: %w", err)
	}
	compactedHead, err := readCopyDBHead(chainDb)
	if err != nil {
		return fmt.Errorf("read compacted target head: %w", err)
	}
	if compactedHead != sourceHead {
		return fmt.Errorf("compacted target head no longer matches source head")
	}
	if _, err := state.New(sourceHead.root, state.NewDatabaseWithCache(chainDb, 0)); err != nil {
		return fmt.Errorf("reopen compacted target head state: %w", err)
	}
	fmt.Printf("Compaction done in %v.\n\n", time.Since(start))
	return nil
}

func copyPrunedChain(sourceDb, targetDb ethdb.Database, chain *core.BlockChain, sourceHeaders *core.HeaderChain, sourceHead copyDBHead, fullTail, stateCache uint64) error {
	if sourceHead.number == 0 {
		return verifyCopyDBHead(sourceHead, chain.CurrentBlock())
	}
	sourceHeader := sourceHeaders.GetHeader(sourceHead.hash, sourceHead.number)
	if sourceHeader == nil {
		return fmt.Errorf("source head header #%d %s is missing", sourceHead.number, sourceHead.hash)
	}
	sourceTd := sourceHeaders.GetTd(sourceHead.hash, sourceHead.number)
	if sourceTd == nil {
		return fmt.Errorf("source head total difficulty #%d %s is missing", sourceHead.number, sourceHead.hash)
	}

	start := time.Now()
	if err := copyDBHeaders(sourceDb, targetDb, chain, sourceHeaders, sourceHeader, sourceTd); err != nil {
		return err
	}
	fmt.Printf("Header copy done in %v\n", time.Since(start))
	if current := chain.CurrentHeader(); current.Number.Uint64() != sourceHead.number || current.Hash() != sourceHead.hash {
		return fmt.Errorf("copied header head mismatch: have #%d %s, want #%d %s", current.Number.Uint64(), current.Hash(), sourceHead.number, sourceHead.hash)
	}

	pivotNumber := uint64(0)
	if sourceHead.number > fullTail {
		pivotNumber = sourceHead.number - fullTail
	}
	pivotHeader := sourceHeaders.GetHeaderByNumber(pivotNumber)
	if pivotHeader == nil {
		return fmt.Errorf("source pivot header #%d is missing", pivotNumber)
	}

	start = time.Now()
	if err := copyDBState(sourceDb, targetDb, chain, sourceHeaders, pivotHeader.Root, stateCache); err != nil {
		return fmt.Errorf("copy pivot state #%d %s: %w", pivotNumber, pivotHeader.Root, err)
	}
	if _, err := state.New(pivotHeader.Root, state.NewDatabaseWithCache(targetDb, 0)); err != nil {
		return fmt.Errorf("open copied pivot state #%d %s: %w", pivotNumber, pivotHeader.Root, err)
	}
	fmt.Printf("State copy done in %v at pivot #%d\n", time.Since(start), pivotNumber)

	if pivotNumber > 0 {
		pivotBlock, pivotReceipts, err := readCopyDBBlock(sourceDb, pivotHeader)
		if err != nil {
			return err
		}
		if index, err := chain.InsertReceiptChain(types.Blocks{pivotBlock}, []types.Receipts{pivotReceipts}, 0); err != nil {
			return fmt.Errorf("insert pivot block #%d (index %d): %w", pivotNumber, index, err)
		}
		if err := chain.FastSyncCommitHead(pivotBlock.Hash()); err != nil {
			return fmt.Errorf("commit pivot block #%d: %w", pivotNumber, err)
		}
	}

	start = time.Now()
	if err := copyDBFullTail(sourceDb, chain, sourceHeaders, pivotHeader, sourceHead.number); err != nil {
		return err
	}
	fmt.Printf("Full tail import done in %v (%d blocks)\n", time.Since(start), sourceHead.number-pivotNumber)

	if pivotNumber > 1 {
		oldNumber := pivotNumber - 1
		oldHash := rawdb.ReadCanonicalHash(targetDb, oldNumber)
		if oldHash == (common.Hash{}) {
			return fmt.Errorf("copied canonical header #%d is missing", oldNumber)
		}
		if rawdb.HasBody(targetDb, oldHash, oldNumber) || rawdb.HasReceipts(targetDb, oldHash, oldNumber) {
			return fmt.Errorf("historical block content before pivot was copied at #%d", oldNumber)
		}
	}
	return verifyCopyDBHead(sourceHead, chain.CurrentBlock())
}

func copyDBHeaders(sourceDb, targetDb ethdb.Database, chain *core.BlockChain, sourceHeaders *core.HeaderChain, head *types.Header, td *big.Int) error {
	dl := downloader.New(0, targetDb, nil, new(event.TypeMux), chain, nil, nil, nil, nil)
	defer dl.Terminate()
	peer := downloader.NewFakePeer("local", sourceDb, sourceHeaders, dl)
	if err := dl.RegisterPeer("local", 63, peer); err != nil {
		return fmt.Errorf("register local header peer: %w", err)
	}
	if err := dl.Synchronise("local", head.Hash(), td, downloader.LightSync); err != nil {
		return fmt.Errorf("copy canonical headers: %w", err)
	}
	return nil
}

func copyDBState(sourceDb, targetDb ethdb.Database, chain *core.BlockChain, sourceHeaders *core.HeaderChain, root common.Hash, cache uint64) error {
	bloom := trie.NewSyncBloom(cache, targetDb)
	defer bloom.Close()
	dl := downloader.New(0, targetDb, bloom, new(event.TypeMux), chain, nil, nil, nil, nil)
	defer dl.Terminate()
	peer := downloader.NewFakePeer("local", sourceDb, sourceHeaders, dl)
	if err := dl.RegisterPeer("local", 63, peer); err != nil {
		return fmt.Errorf("register local state peer: %w", err)
	}
	if err := dl.SynchroniseState("local", root); err != nil {
		return fmt.Errorf("synchronise state: %w", err)
	}
	return nil
}

func copyDBFullTail(sourceDb ethdb.Database, chain *core.BlockChain, sourceHeaders *core.HeaderChain, pivot *types.Header, head uint64) error {
	const batchSize = uint64(256)

	parentHash := pivot.Hash()
	for first := pivot.Number.Uint64() + 1; first <= head; first += batchSize {
		last := first + batchSize - 1
		if last > head {
			last = head
		}
		blocks := make(types.Blocks, 0, last-first+1)
		for number := first; number <= last; number++ {
			header := sourceHeaders.GetHeaderByNumber(number)
			if header == nil {
				return fmt.Errorf("source tail header #%d is missing", number)
			}
			block, _, err := readCopyDBBlock(sourceDb, header)
			if err != nil {
				return err
			}
			if block.ParentHash() != parentHash {
				return fmt.Errorf("source tail is not contiguous at #%d", number)
			}
			blocks = append(blocks, block)
			parentHash = block.Hash()
		}
		if index, err := chain.InsertChain(blocks); err != nil {
			failed := first
			if index >= 0 && index < len(blocks) {
				failed = blocks[index].NumberU64()
			}
			return fmt.Errorf("execute full tail at #%d: %w", failed, err)
		}
	}
	return nil
}

func readCopyDBBlock(db ethdb.Database, header *types.Header) (*types.Block, types.Receipts, error) {
	number, hash := header.Number.Uint64(), header.Hash()
	if !rawdb.HasBody(db, hash, number) {
		return nil, nil, fmt.Errorf("source body #%d %s is missing", number, hash)
	}
	block := rawdb.ReadBlock(db, hash, number)
	if block == nil {
		return nil, nil, fmt.Errorf("read source block #%d %s", number, hash)
	}
	if have := types.DeriveSha(block.Transactions()); have != header.TxHash {
		return nil, nil, fmt.Errorf("source body transaction root mismatch at #%d %s: have %s want %s", number, hash, have, header.TxHash)
	}
	if have := types.CalcUncleHash(block.Uncles()); have != header.UncleHash {
		return nil, nil, fmt.Errorf("source body uncle hash mismatch at #%d %s: have %s want %s", number, hash, have, header.UncleHash)
	}
	if !rawdb.HasReceipts(db, hash, number) {
		return nil, nil, fmt.Errorf("source receipts #%d %s are missing", number, hash)
	}
	receipts := rawdb.ReadRawReceipts(db, hash, number)
	if have := types.DeriveSha(receipts); have != header.ReceiptHash {
		return nil, nil, fmt.Errorf("source receipt root mismatch at #%d %s: have %s want %s", number, hash, have, header.ReceiptHash)
	}
	return block, receipts, nil
}

type copyDBHead struct {
	number uint64
	hash   common.Hash
	root   common.Hash
}

func readCopyDBHead(db ethdb.Database) (copyDBHead, error) {
	headerHash := rawdb.ReadHeadHeaderHash(db)
	blockHash := rawdb.ReadHeadBlockHash(db)
	fastHash := rawdb.ReadHeadFastBlockHash(db)
	if headerHash == (common.Hash{}) {
		return copyDBHead{}, fmt.Errorf("missing head header hash")
	}
	if headerHash != blockHash || headerHash != fastHash {
		return copyDBHead{}, fmt.Errorf("head header, block and fast block are not aligned")
	}
	number := rawdb.ReadHeaderNumber(db, headerHash)
	if number == nil {
		return copyDBHead{}, fmt.Errorf("missing number for head %s", headerHash)
	}
	header := rawdb.ReadHeader(db, headerHash, *number)
	if header == nil {
		return copyDBHead{}, fmt.Errorf("missing header %d %s", *number, headerHash)
	}
	if rawdb.ReadTd(db, headerHash, *number) == nil {
		return copyDBHead{}, fmt.Errorf("missing total difficulty for head %d %s", *number, headerHash)
	}
	return copyDBHead{number: *number, hash: headerHash, root: header.Root}, nil
}

func validateCopyDBTarget(source ethdb.Database, target ethdb.Database) error {
	sourceGenesis := rawdb.ReadCanonicalHash(source, 0)
	if sourceGenesis == (common.Hash{}) {
		return fmt.Errorf("source database has no genesis")
	}
	targetGenesis := rawdb.ReadCanonicalHash(target, 0)
	if targetGenesis == (common.Hash{}) {
		return fmt.Errorf("target datadir is not initialized; run geth init with the Metis genesis first")
	}
	if sourceGenesis != targetGenesis {
		return fmt.Errorf("source and target genesis hashes differ: source %s target %s", sourceGenesis, targetGenesis)
	}
	sourceConfig, targetConfig := rawdb.ReadChainConfig(source, sourceGenesis), rawdb.ReadChainConfig(target, targetGenesis)
	if sourceConfig == nil || targetConfig == nil {
		return fmt.Errorf("source or target chain config is missing")
	}
	sourceJSON, err := json.Marshal(sourceConfig)
	if err != nil {
		return fmt.Errorf("encode source chain config: %w", err)
	}
	targetJSON, err := json.Marshal(targetConfig)
	if err != nil {
		return fmt.Errorf("encode target chain config: %w", err)
	}
	if !bytes.Equal(sourceJSON, targetJSON) {
		return fmt.Errorf("source and target chain configs differ")
	}
	targetHead, err := readCopyDBHead(target)
	if err != nil {
		return fmt.Errorf("read target head: %w", err)
	}
	if targetHead.number != 0 || targetHead.hash != targetGenesis {
		return fmt.Errorf("target database is not fresh; expected genesis-only datadir")
	}
	return nil
}

func verifyCopyDBHead(source copyDBHead, target *types.Block) error {
	if target == nil {
		return fmt.Errorf("target head block is missing")
	}
	if source.number != target.NumberU64() || source.hash != target.Hash() || source.root != target.Root() {
		return fmt.Errorf("copied head mismatch: source #%d %s root %s, target #%d %s root %s",
			source.number, source.hash, source.root, target.NumberU64(), target.Hash(), target.Root())
	}
	return nil
}

func validateCopyDBPaths(sourceChaindata, sourceAncient, targetChaindata, targetAncient string) error {
	sourceChaindata = canonicalCopyDBPath(sourceChaindata)
	sourceAncient = canonicalCopyDBPath(sourceAncient)
	targetChaindata = canonicalCopyDBPath(targetChaindata)
	targetAncient = canonicalCopyDBPath(targetAncient)
	if copyDBPathsOverlap(sourceChaindata, targetChaindata) {
		return fmt.Errorf("source and target chaindata paths overlap: %s and %s", sourceChaindata, targetChaindata)
	}
	if copyDBPathsOverlap(sourceAncient, targetAncient) {
		return fmt.Errorf("source and target ancient paths overlap: %s and %s", sourceAncient, targetAncient)
	}
	return nil
}

func canonicalCopyDBPath(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		return resolved
	}
	return filepath.Clean(absolute)
}

func copyDBPathsOverlap(first, second string) bool {
	if first == second {
		return true
	}
	relative, err := filepath.Rel(first, second)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return true
	}
	relative, err = filepath.Rel(second, first)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func removeDB(ctx *cli.Context) error {
	stack, config := makeConfigNode(ctx)

	// Remove the full node state database
	path := stack.ResolvePath("chaindata")
	if common.FileExist(path) {
		confirmAndRemoveDB(path, "full node state database")
	} else {
		log.Info("Full node state database missing", "path", path)
	}
	// Remove the full node ancient database
	path = config.Eth.DatabaseFreezer
	switch {
	case path == "":
		path = filepath.Join(stack.ResolvePath("chaindata"), "ancient")
	case !filepath.IsAbs(path):
		path = config.Node.ResolvePath(path)
	}
	if common.FileExist(path) {
		confirmAndRemoveDB(path, "full node ancient database")
	} else {
		log.Info("Full node ancient database missing", "path", path)
	}
	// Remove the light node database
	path = stack.ResolvePath("lightchaindata")
	if common.FileExist(path) {
		confirmAndRemoveDB(path, "light node database")
	} else {
		log.Info("Light node database missing", "path", path)
	}
	return nil
}

// confirmAndRemoveDB prompts the user for a last confirmation and removes the
// folder if accepted.
func confirmAndRemoveDB(database string, kind string) {
	confirm, err := console.Stdin.PromptConfirm(fmt.Sprintf("Remove %s (%s)?", kind, database))
	switch {
	case err != nil:
		utils.Fatalf("%v", err)
	case !confirm:
		log.Info("Database deletion skipped", "path", database)
	default:
		start := time.Now()
		filepath.Walk(database, func(path string, info os.FileInfo, err error) error {
			// If we're at the top level folder, recurse into
			if path == database {
				return nil
			}
			// Delete all the files, but not subfolders
			if !info.IsDir() {
				os.Remove(path)
				return nil
			}
			return filepath.SkipDir
		})
		log.Info("Database successfully deleted", "path", database, "elapsed", common.PrettyDuration(time.Since(start)))
	}
}

func dump(ctx *cli.Context) error {
	stack := makeFullNode(ctx)
	defer stack.Close()

	chain, chainDb := utils.MakeChain(ctx, stack)
	defer chainDb.Close()
	for _, arg := range ctx.Args() {
		var block *types.Block
		if hashish(arg) {
			block = chain.GetBlockByHash(common.HexToHash(arg))
		} else {
			num, _ := strconv.Atoi(arg)
			block = chain.GetBlockByNumber(uint64(num))
		}
		if block == nil {
			fmt.Println("{}")
			utils.Fatalf("block not found")
		} else {
			state, err := state.New(block.Root(), state.NewDatabase(chainDb))
			if err != nil {
				utils.Fatalf("could not create new state: %v", err)
			}
			excludeCode := ctx.Bool(utils.ExcludeCodeFlag.Name)
			excludeStorage := ctx.Bool(utils.ExcludeStorageFlag.Name)
			includeMissing := ctx.Bool(utils.IncludeIncompletesFlag.Name)
			if ctx.Bool(utils.IterativeOutputFlag.Name) {
				state.IterativeDump(excludeCode, excludeStorage, !includeMissing, json.NewEncoder(os.Stdout))
			} else {
				if includeMissing {
					fmt.Printf("If you want to include accounts with missing preimages, you need iterative output, since" +
						" otherwise the accounts will overwrite each other in the resulting mapping.")
				}
				fmt.Printf("%v %s\n", includeMissing, state.Dump(excludeCode, excludeStorage, false))
			}
		}
	}
	return nil
}

func inspect(ctx *cli.Context) error {
	node, _ := makeConfigNode(ctx)
	defer node.Close()

	_, chainDb := utils.MakeChain(ctx, node)
	defer chainDb.Close()

	return rawdb.InspectDatabase(chainDb)
}

// hashish returns true for strings that look like hashes.
func hashish(x string) bool {
	_, err := strconv.Atoi(x)
	return err != nil
}

func fetchGenesis(url string, timeout time.Duration) ([]byte, error) {
	client := &http.Client{
		Timeout: timeout,
	}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return ioutil.ReadAll(resp.Body)
}
