package eth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"sort"
	"strings"

	ptracer "github.com/Chaintable/pipeline/tracer"
	ptypes "github.com/Chaintable/pipeline/types"
	"github.com/Chaintable/pipeline/util"
	"github.com/MetisProtocol/mvm/l2geth/common"
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
		// Convert GenesisAlloc to ptypes.GenesisAlloc
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

		// 构造 genesis tx 和 trace
		zeroAddr := "0x0000000000000000000000000000000000000000"
		txIdx := int64(0)

		// 对地址排序，确保遍历顺序确定性
		sortedAddrs := make([]common.Address, 0, len(genesis))
		for addr := range genesis {
			sortedAddrs = append(sortedAddrs, addr)
		}
		sort.Slice(sortedAddrs, func(i, j int) bool {
			return sortedAddrs[i].Hex() < sortedAddrs[j].Hex()
		})

		for _, addr := range sortedAddrs {
			account := genesis[addr]
			addrLower := strings.ToLower(addr.Hex())

			// 处理有 Storage 的账户
			if len(account.Storage) > 0 {
				blockFile.StorageContracts = append(blockFile.StorageContracts, addrLower)
			}

			// 处理有 balance 的账户 - 构造转账 tx 和 call trace
			if account.Balance != nil && account.Balance.Sign() > 0 {
				// tx id: 0xgenesis01 + 13个0 + 地址(42字符) = 66字符
				txID := fmt.Sprintf("0xgenesis01%013d%s", 0, addrLower)

				tx := ptypes.Transaction{
					ID:               txID,
					From:             zeroAddr,
					To:               addrLower,
					Gas:              big.NewInt(0),
					GasPrice:         big.NewInt(0),
					GasUsed:          big.NewInt(0),
					Status:           true,
					GasFeeCap:        big.NewInt(0),
					GasTipCap:        big.NewInt(0),
					Input:            []byte{},
					Nonce:            big.NewInt(0),
					TransactionIndex: txIdx,
					Value:            (*hexutil.Big)(account.Balance),
				}
				blockFile.Txs = append(blockFile.Txs, tx)

				// trace id = hash(tx_id, parent_trace_id, pos_in_parent_trace)
				traceID := util.ToHash([]string{txID, "", "0"})
				trace := ptypes.Trace{
					ID:                traceID,
					From:              zeroAddr,
					Gas:               big.NewInt(0),
					Input:             []byte{},
					To:                addrLower,
					Value:             (*hexutil.Big)(account.Balance),
					GasUsed:           big.NewInt(0),
					Output:            []byte{},
					CallCreateType:    "call",
					CallType:          "call",
					TxID:              txID,
					ParentTraceID:     "",
					PosInParentTrace:  0,
					SelfStorageChange: false,
					StorageChange:     false,
					Subtraces:         0,
					TraceAddress:      []int64{},
				}
				blockFile.Traces = append(blockFile.Traces, trace)
				txIdx++
			}

			// 处理有 code 的账户 - 构造 create tx 和 create trace
			if len(account.Code) > 0 {
				// tx id: 0xgenesis02 + 13个0 + 地址(42字符) = 66字符
				txID := fmt.Sprintf("0xgenesis02%013d%s", 0, addrLower)

				tx := ptypes.Transaction{
					ID:               txID,
					From:             zeroAddr,
					To:               addrLower,
					Gas:              big.NewInt(0),
					GasPrice:         big.NewInt(0),
					GasUsed:          big.NewInt(0),
					Status:           true,
					GasFeeCap:        big.NewInt(0),
					GasTipCap:        big.NewInt(0),
					Input:            account.Code,
					Nonce:            big.NewInt(0),
					TransactionIndex: txIdx,
					Value:            (*hexutil.Big)(big.NewInt(0)),
				}
				blockFile.Txs = append(blockFile.Txs, tx)

				// trace id = hash(tx_id, parent_trace_id, pos_in_parent_trace)
				traceID := util.ToHash([]string{txID, "", "0"})
				trace := ptypes.Trace{
					ID:                traceID,
					From:              zeroAddr,
					Gas:               big.NewInt(0),
					Input:             account.Code,
					To:                addrLower,
					Value:             (*hexutil.Big)(big.NewInt(0)),
					GasUsed:           big.NewInt(0),
					Output:            account.Code, // output 直接使用 input (code)
					CallCreateType:    "create",
					CallType:          "",
					TxID:              txID,
					ParentTraceID:     "",
					PosInParentTrace:  0,
					SelfStorageChange: false,
					StorageChange:     false,
					Subtraces:         0,
					TraceAddress:      []int64{},
				}
				blockFile.Traces = append(blockFile.Traces, trace)
				txIdx++
			}
		}

		// 添加原生代币合约创建 tx 和 trace (E地址)
		nativeTokenAddr := "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
		nativeTokenTxID := fmt.Sprintf("0xgenesis03%013d%s", 0, nativeTokenAddr)

		nativeTokenTx := ptypes.Transaction{
			ID:               nativeTokenTxID,
			From:             zeroAddr,
			To:               nativeTokenAddr,
			Gas:              big.NewInt(0),
			GasPrice:         big.NewInt(0),
			GasUsed:          big.NewInt(0),
			Status:           true,
			GasFeeCap:        big.NewInt(0),
			GasTipCap:        big.NewInt(0),
			Input:            []byte{},
			Nonce:            big.NewInt(0),
			TransactionIndex: txIdx,
			Value:            (*hexutil.Big)(big.NewInt(0)),
		}
		blockFile.Txs = append(blockFile.Txs, nativeTokenTx)

		nativeTokenTraceID := util.ToHash([]string{nativeTokenTxID, "", "0"})
		nativeTokenTrace := ptypes.Trace{
			ID:                nativeTokenTraceID,
			From:              zeroAddr,
			Gas:               big.NewInt(0),
			Input:             []byte{},
			To:                nativeTokenAddr,
			Value:             (*hexutil.Big)(big.NewInt(0)),
			GasUsed:           big.NewInt(0),
			Output:            []byte{},
			CallCreateType:    "create",
			CallType:          "",
			TxID:              nativeTokenTxID,
			ParentTraceID:     "",
			PosInParentTrace:  0,
			SelfStorageChange: false,
			StorageChange:     false,
			Subtraces:         0,
			TraceAddress:      []int64{},
		}
		blockFile.Traces = append(blockFile.Traces, nativeTokenTrace)

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

	// Prepare base state
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

	// Mutate the block and state according to any hard-fork specs
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

	// Set hooks for log tracing
	statedb.OnLog = rpcTracer.OnLog

	for i, tx := range txs {
		statedb.Prepare(tx.Hash(), block.Hash(), i)

		msg, err := tx.AsMessage(signer)
		if err != nil {
			return nil, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
		}

		rpcTracer.OnTxStart(tx, msg.From())

		// Create EVM context
		evmCtx := core.NewEVMContext(msg, header, api.eth.blockchain, nil)
		vmenv := vm.NewEVM(evmCtx, statedb, chainConfig, vmConfig)

		// Compute the fee related information that is to be included
		// on the receipt. This must happen before the state transition
		// to ensure that the correct information is used.
		l1Fee, l1GasPrice, l1GasUsed, scalar, err := fees.DeriveL1GasInfo(msg, statedb)
		if err != nil {
			return nil, fmt.Errorf("could not derive L1 gas info for tx %d [%v]: %w", i, tx.Hash().Hex(), err)
		}

		// Apply the transaction
		_, gas, failed, err := core.ApplyMessageWithBlockNumber(vmenv, msg, gp, header.Number.Uint64())
		if err != nil {
			return nil, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
		}

		// Update the state with pending changes
		var root []byte
		if chainConfig.IsByzantium(header.Number) {
			statedb.Finalise(true)
		} else {
			root = statedb.IntermediateRoot(chainConfig.IsEIP158(header.Number)).Bytes()
		}
		*usedGas += gas

		// Create receipt
		receipt := types.NewReceipt(root, failed, *usedGas)
		receipt.L1GasPrice = l1GasPrice
		receipt.L1GasUsed = l1GasUsed
		receipt.L1Fee = l1Fee
		receipt.FeeScalar = scalar
		receipt.TxHash = tx.Hash()
		receipt.GasUsed = gas
		// if the transaction created a contract, store the creation address in the receipt.
		if msg.To() == nil {
			if rcfg.UsingOVM {
				sysAddress := rcfg.SystemAddressFor(chainConfig.ChainID, vmenv.Context.Origin)
				// If nonce is zero, and the deployer is a system address deployer,
				// set the provided system contract address.
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

	// Finalize the block, applying any consensus engine specific extras (e.g. block rewards)
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
