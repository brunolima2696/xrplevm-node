package cmd

import (
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/server"
	evmmempool "github.com/cosmos/evm/mempool"
	evmrpc "github.com/cosmos/evm/rpc"
	"github.com/cosmos/evm/rpc/stream"
	servertypes "github.com/cosmos/evm/server/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/xrplevm/node/v10/x/fabricevents"
)

// Read-only namespace for inner EVM transactions carried by IBC. We deliberately
// do NOT fabricate eth receipts or fake an independent transaction in the Comet
// block: the enclosing on-chain transaction is MsgRecvPacket.
type fabricTransactionAPI struct{ client client.Context }

func (api *fabricTransactionAPI) GetTransactionByHash(hash common.Hash) (*fabricevents.SignedTransactionRecord, error) {
	return fabricevents.QuerySignedTransaction(api.client, hash.Hex())
}
func (api *fabricTransactionAPI) GetTransactionResult(hash common.Hash) (*fabricevents.SignedTransactionResult, error) {
	record, err := fabricevents.QuerySignedTransaction(api.client, hash.Hex())
	if err != nil || record == nil {
		return nil, err
	}
	return &record.Result, nil
}

func init() {
	err := evmrpc.RegisterAPINamespace("fabric", func(_ *server.Context, ctx client.Context, _ *stream.RPCStream, _ bool, _ servertypes.EVMTxIndexer, _ *evmmempool.ExperimentalEVMMempool) []rpc.API {
		return []rpc.API{{Namespace: "fabric", Version: "1.0", Service: &fabricTransactionAPI{client: ctx}, Public: true}}
	})
	if err != nil {
		panic(err)
	}
}
