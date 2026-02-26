package eth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	ptracer "github.com/Chaintable/pipeline/tracer"
	ptypes "github.com/Chaintable/pipeline/types"
	"github.com/Chaintable/pipeline/util"
	"github.com/MetisProtocol/mvm/l2geth/common/hexutil"
	"github.com/MetisProtocol/mvm/l2geth/consensus/misc"
	"github.com/MetisProtocol/mvm/l2geth/core"
	"github.com/MetisProtocol/mvm/l2geth/core/types"
	"github.com/MetisProtocol/mvm/l2geth/core/vm"
	"github.com/MetisProtocol/mvm/l2geth/crypto"
	"github.com/MetisProtocol/mvm/l2geth/log"
	"github.com/MetisProtocol/mvm/l2geth/rollup/fees"
	"github.com/MetisProtocol/mvm/l2geth/rollup/rcfg"
	"github.com/MetisProtocol/mvm/l2geth/rpc"
)

type DebankAPI struct {
	eth *Ethereum
}

func NewDebankAPI(eth *Ethereum) *DebankAPI {
	return &DebankAPI{
		eth: eth,
	}
}

func getGenesisState() (alloc core.GenesisAlloc, err error) {
	url := os.Getenv("ROLLUP_STATE_DUMP_PATH")
	if len(url) == 0 {
		url = "https://metisprotocol.github.io/metis-networks/andromeda-mainnet/state-dump.latest.json"
	}
	client := &http.Client{}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	genesis := new(core.Genesis)
	if err := json.Unmarshal(data, genesis); err != nil {
		return nil, fmt.Errorf("failed to unmarshal genesis state: %w", err)
	}
	return genesis.Alloc, nil
}

func (api *DebankAPI) DebankBlock(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*ptypes.DebankOutPut, error) {
	block, err := api.eth.APIBackend.BlockByNumberOrHash(ctx, blockNrOrHash)
	if err != nil {
		return nil, err
	}
	if block == nil {
		return nil, fmt.Errorf("block not found")
	}
	if block.NumberU64() == 0 {
		genesis, err := getGenesisState()
		if err != nil {
			return nil, fmt.Errorf("failed to get genesis state: %w", err)
		}
		header := util.BuildPilelineBlockHeader(block)
		palloc := make(ptypes.GenesisAlloc, len(genesis))
		for addr, acc := range genesis {
			palloc[addr] = ptypes.GenesisAccount{
				Balance: acc.Balance,
				Code:    acc.Code,
				Storage: acc.Storage,
				Nonce:   acc.Nonce,
			}
		}
		blockDiff := ptracer.GenesisAllocToStateDiff(palloc)
		blockDiff.Hash = header.StateRoot
		blockFile := &ptypes.BlockFile{
			Block:            util.BuildPipelineBlock(block),
			Txs:              make([]ptypes.Transaction, 0),
			Events:           make([]ptypes.Event, 0),
			Traces:           make([]ptypes.Trace, 0),
			ErrorEvents:      make([]ptypes.Event, 0),
			ErrorTraces:      make([]ptypes.Trace, 0),
			StorageContracts: make([]string, 0),
		}
		for addr, account := range genesis {
			if len(account.Storage) > 0 {
				blockFile.StorageContracts = append(blockFile.StorageContracts, strings.ToLower(addr.Hex()))
			}
		}
		var stateDiffBytes []byte
		if blockDiff != nil {
			stateDiffBytes, err = util.EncodeToRlp(blockDiff)
			if err != nil {
				log.Error("Failed to encode state diff", "err", err)
				stateDiffBytes = []byte{}
			}
		} else {
			stateDiffBytes = []byte{}
		}

		return &ptypes.DebankOutPut{
			BlockFile:      blockFile,
			Header:         header,
			StateDiff:      hexutil.Bytes(stateDiffBytes),
			ValidationHash: blockFile.Validation().ValidationHash,
		}, nil
	}

	parent := api.eth.blockchain.GetBlock(block.ParentHash(), block.NumberU64()-1)
	if parent == nil {
		return nil, fmt.Errorf("parent block not found")
	}
	statedb, err := api.eth.blockchain.StateAt(parent.Root())
	if err != nil {
		return nil, err
	}

	rpcTracer := ptracer.RPCTracer{}
	vmConfig := vm.Config{
		Debug:     true,
		Tracer:    &rpcTracer,
		TracerExt: &rpcTracer,
	}

	rpcTracer.OnBlockStart(block)

	chainConfig := api.eth.blockchain.Config()
	if chainConfig.DAOForkSupport && chainConfig.DAOForkBlock != nil && chainConfig.DAOForkBlock.Cmp(block.Number()) == 0 {
		misc.ApplyDAOHardFork(statedb)
	}

	var (
		txs     = block.Transactions()
		header  = block.Header()
		signer  = types.MakeSigner(chainConfig, block.Number())
		gp      = new(core.GasPool).AddGas(block.GasLimit())
		usedGas = new(uint64)
	)

	statedb.OnLog = rpcTracer.OnLog

	for i, tx := range txs {
		statedb.Prepare(tx.Hash(), block.Hash(), i)

		msg, err := tx.AsMessage(signer)
		if err != nil {
			return nil, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
		}

		rpcTracer.OnTxStart(tx, msg.From())

		evmCtx := core.NewEVMContext(msg, header, api.eth.blockchain, nil)
		vmenv := vm.NewEVM(evmCtx, statedb, chainConfig, vmConfig)

		l1Fee, l1GasPrice, l1GasUsed, scalar, err := fees.DeriveL1GasInfo(msg, statedb)
		if err != nil {
			return nil, fmt.Errorf("could not derive L1 gas info for tx %d [%v]: %w", i, tx.Hash().Hex(), err)
		}

		_, gas, failed, err := core.ApplyMessageWithBlockNumber(vmenv, msg, gp, header.Number.Uint64())
		if err != nil {
			return nil, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
		}

		var root []byte
		if chainConfig.IsByzantium(header.Number) {
			statedb.Finalise(true)
		} else {
			root = statedb.IntermediateRoot(chainConfig.IsEIP158(header.Number)).Bytes()
		}
		*usedGas += gas

		receipt := types.NewReceipt(root, failed, *usedGas)
		receipt.L1GasPrice = l1GasPrice
		receipt.L1GasUsed = l1GasUsed
		receipt.L1Fee = l1Fee
		receipt.FeeScalar = scalar
		receipt.TxHash = tx.Hash()
		receipt.GasUsed = gas
		if msg.To() == nil {
			if rcfg.UsingOVM {
				sysAddress := rcfg.SystemAddressFor(chainConfig.ChainID, vmenv.Context.Origin)
				if sysAddress != rcfg.ZeroSystemAddress && tx.Nonce() == 0 && tx.To() == nil {
					receipt.ContractAddress = sysAddress
				} else {
					receipt.ContractAddress = crypto.CreateAddress(vmenv.Context.Origin, tx.Nonce())
				}
			} else {
				receipt.ContractAddress = crypto.CreateAddress(vmenv.Context.Origin, tx.Nonce())
			}
		}
		receipt.Logs = statedb.GetLogs(tx.Hash())
		receipt.Bloom = types.CreateBloom(types.Receipts{receipt})
		receipt.BlockHash = statedb.BlockHash()
		receipt.BlockNumber = header.Number
		receipt.TransactionIndex = uint(statedb.TxIndex())

		rpcTracer.OnTxEnd(receipt, nil)
	}

	api.eth.engine.Finalize(api.eth.blockchain, header, statedb, block.Transactions(), block.Uncles())

	root, destructs, accounts, storages, codes, err := statedb.StateDiff(chainConfig.IsEIP158(block.Number()))
	if err != nil {
		return nil, fmt.Errorf("could not get state diff: %w", err)
	}

	if root != block.Header().Root {
		return nil, fmt.Errorf("state root mismatch: expected %x, got %x", block.Header().Root, root)
	}

	parentRoot := parent.Root()
	res := rpcTracer.GetOutPut(parentRoot, root, destructs, accounts, storages, codes)

	return res, nil
}
