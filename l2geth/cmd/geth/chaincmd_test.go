package main

import (
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/MetisProtocol/mvm/l2geth/core"
	"github.com/MetisProtocol/mvm/l2geth/core/rawdb"
	"github.com/MetisProtocol/mvm/l2geth/params"
)

func TestChainInit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		f, err := os.Open("testdata/init.json")
		if err != nil {
			panic(err)
		}
		defer f.Close()
		io.Copy(w, f)
	}))

	tests := []struct {
		name     string
		url      string
		hash     string
		errorMsg string
	}{
		{
			"no genesis hash specified",
			server.URL,
			"",
			"Must specify a genesis hash argument if the genesis path argument is an URL",
		},
		{
			"invalid genesis hash specified",
			server.URL,
			"not hex yo",
			"Error decoding genesis hash",
		},
		{
			"bad URL",
			"https://honk",
			"0x1234",
			"Failed to fetch genesis file",
		},
		{
			"mis-matched hashes",
			server.URL,
			"0x1234",
			"Genesis hashes do not match",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			datadir := tmpdir(t)
			geth := runGeth(t, "init", tt.url, tt.hash, "--datadir", datadir)
			geth.ExpectRegexp(tt.errorMsg)
		})
	}

	t.Run("URL and hash args OK", func(t *testing.T) {
		datadir := tmpdir(t)
		geth := runGeth(t, "init", server.URL, "0x1f0201852c30e203a701ac283aeafafaf55b2ad3ae2f4e8f15c61e761434fb62", "--datadir", datadir)
		geth.ExpectExit()
		geth = runGeth(t, "dump-chain-cfg", "--datadir", datadir)
		geth.ExpectRegexp("\"muirGlacierBlock\": 500")
	})

	t.Run("file arg OK", func(t *testing.T) {
		datadir := tmpdir(t)
		geth := runGeth(t, "init", "testdata/init.json", "--datadir", datadir)
		geth.ExpectExit()
		geth = runGeth(t, "dump-chain-cfg", "--datadir", datadir)
		geth.ExpectRegexp("\"muirGlacierBlock\": 500")
	})
}

func TestDumpChainCfg(t *testing.T) {
	datadir := tmpdir(t)
	geth := runGeth(t, "init", "testdata/init.json", "--datadir", datadir)
	geth.ExpectExit()
	geth = runGeth(t, "dump-chain-cfg", "--datadir", datadir)
	geth.Expect(`{
  "chainId": 69,
  "homesteadBlock": 0,
  "eip150Block": 0,
  "eip150Hash": "0x0000000000000000000000000000000000000000000000000000000000000000",
  "eip155Block": 0,
  "eip158Block": 0,
  "byzantiumBlock": 0,
  "constantinopleBlock": 0,
  "petersburgBlock": 0,
  "istanbulBlock": 0,
  "muirGlacierBlock": 500,
  "clique": {
    "period": 0,
    "epoch": 30000
  }
	}`)
}

func TestValidateCopyDBTarget(t *testing.T) {
	genesis := &core.Genesis{Config: params.AllEthashProtocolChanges}
	source := rawdb.NewMemoryDatabase()
	defer source.Close()
	target := rawdb.NewMemoryDatabase()
	defer target.Close()
	genesis.MustCommit(source)
	genesis.MustCommit(target)

	if err := validateCopyDBTarget(source, target); err != nil {
		t.Fatalf("matching initialized target rejected: %v", err)
	}

	wrongConfig := *rawdb.ReadChainConfig(target, rawdb.ReadCanonicalHash(target, 0))
	wrongConfig.ChainID = big.NewInt(999)
	rawdb.WriteChainConfig(target, rawdb.ReadCanonicalHash(target, 0), &wrongConfig)
	if err := validateCopyDBTarget(source, target); err == nil {
		t.Fatal("target with mismatched chain config was accepted")
	}
}

func TestValidateCopyDBTargetRejectsUninitializedTarget(t *testing.T) {
	source := rawdb.NewMemoryDatabase()
	defer source.Close()
	target := rawdb.NewMemoryDatabase()
	defer target.Close()
	(&core.Genesis{Config: params.AllEthashProtocolChanges}).MustCommit(source)

	if err := validateCopyDBTarget(source, target); err == nil {
		t.Fatal("uninitialized target was accepted")
	}
}

func TestValidateCopyDBPaths(t *testing.T) {
	root := tmpdir(t)
	source := filepath.Join(root, "source", "chaindata")
	target := filepath.Join(root, "target", "chaindata")
	if err := validateCopyDBPaths(source, filepath.Join(source, "ancient"), target, filepath.Join(target, "ancient")); err != nil {
		t.Fatalf("separate copydb paths rejected: %v", err)
	}
	if err := validateCopyDBPaths(source, filepath.Join(source, "ancient"), source, filepath.Join(source, "ancient")); err == nil {
		t.Fatal("overlapping copydb paths were accepted")
	}
}

func TestVerifyCopyDBHead(t *testing.T) {
	block := (&core.Genesis{Config: params.AllEthashProtocolChanges}).ToBlock(nil)
	head := copyDBHead{number: block.NumberU64(), hash: block.Hash(), root: block.Root()}
	if err := verifyCopyDBHead(head, block); err != nil {
		t.Fatalf("matching head rejected: %v", err)
	}
	head.number++
	if err := verifyCopyDBHead(head, block); err == nil {
		t.Fatal("mismatched head was accepted")
	}
}

func TestCopyDBGenesisOnly(t *testing.T) {
	sourceDatadir := tmpdir(t)
	targetDatadir := tmpdir(t)
	defer os.RemoveAll(sourceDatadir)
	defer os.RemoveAll(targetDatadir)

	geth := runGeth(t, "init", "testdata/init.json", "--datadir", sourceDatadir)
	geth.ExpectRegexp("rcfg UsingOVM[^\\n]*\\n")
	geth.ExpectExit()
	geth = runGeth(t, "init", "testdata/init.json", "--datadir", targetDatadir)
	geth.ExpectRegexp("rcfg UsingOVM[^\\n]*\\n")
	geth.ExpectExit()

	sourceChaindata := filepath.Join(sourceDatadir, "geth", "chaindata")
	sourceAncient := filepath.Join(sourceChaindata, "ancient")
	sourceDB, err := rawdb.NewLevelDBDatabaseWithFreezer(sourceChaindata, 16, 16, sourceAncient, "")
	if err != nil {
		t.Fatalf("open source database: %v", err)
	}
	rawdb.WriteHeadIndex(sourceDB, 101)
	rawdb.WriteHeadQueueIndex(sourceDB, 202)
	if err := sourceDB.Close(); err != nil {
		t.Fatalf("close source database: %v", err)
	}

	geth = runGeth(t,
		"copydb", sourceChaindata, sourceAncient,
		"--datadir", targetDatadir,
		"--syncmode", "fast",
		"--gcmode", "full",
		"--cache", "16",
	)
	geth.ExpectRegexp("(?s).*Compaction done[^\\n]*\\n\\n")
	geth.ExpectExit()

	targetChaindata := filepath.Join(targetDatadir, "geth", "chaindata")
	targetDB, err := rawdb.NewLevelDBDatabaseWithFreezer(targetChaindata, 16, 16, filepath.Join(targetChaindata, "ancient"), "")
	if err != nil {
		t.Fatalf("open target database: %v", err)
	}
	defer targetDB.Close()
	sourceDB, err = rawdb.NewLevelDBDatabaseWithFreezer(sourceChaindata, 16, 16, sourceAncient, "")
	if err != nil {
		t.Fatalf("reopen source database: %v", err)
	}
	defer sourceDB.Close()
	if err := rawdb.VerifyRollupIndexes(sourceDB, targetDB); err != nil {
		t.Fatalf("verify copied rollup indexes: %v", err)
	}
	if got := rawdb.ReadHeadIndex(targetDB); got == nil || *got != 101 {
		t.Fatalf("unexpected copied head index: %v", got)
	}
	if got := rawdb.ReadHeadQueueIndex(targetDB); got == nil || *got != 202 {
		t.Fatalf("unexpected copied queue index: %v", got)
	}
}
