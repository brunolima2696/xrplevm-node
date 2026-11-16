package fabricevents

import (
	"fmt"
	"math"
	"math/big"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	evmante "github.com/cosmos/evm/ante/evm"
	antetypes "github.com/cosmos/evm/ante/types"
	evmexecution "github.com/cosmos/evm/x/vm/ante"
	evmkeeper "github.com/cosmos/evm/x/vm/keeper"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/txpool"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

type preparedSignedTransaction struct {
	sender         common.Address
	price          *big.Int
	fees           sdk.Coins
	baseFeeEnabled bool
}

// prepareSignedTransaction reuses Cosmos EVM's ante helpers, not only its state
// executor. The Cosmos outer envelope checks are not applicable to raw inner txs.
// Only protected legacy, access-list and EIP-1559 txs are supported in this stage.
// Blob and EIP-7702 transactions are explicitly rejected, not partially processed.
func (p *SignedTransactionProcessor) prepareSignedTransaction(ctx sdk.Context, tx *ethtypes.Transaction) (preparedSignedTransaction, error, error) {
	var out preparedSignedTransaction
	if evmtypes.GetChainConfig() == nil {
		return out, nil, fmt.Errorf("EVM chain configuration is not initialized")
	}
	chain := evmtypes.GetEthChainConfig()
	if chain == nil || chain.ChainID == nil {
		return out, nil, fmt.Errorf("EVM chain configuration is not initialized")
	}
	if !tx.Protected() || tx.ChainId().Cmp(chain.ChainID) != 0 {
		return out, fmt.Errorf("unprotected transaction or wrong EVM chain ID"), nil
	}
	if tx.Type() != ethtypes.LegacyTxType && tx.Type() != ethtypes.AccessListTxType && tx.Type() != ethtypes.DynamicFeeTxType {
		return out, fmt.Errorf("unsupported Ethereum transaction type"), nil
	}
	cfg, err := p.evm.EVMConfig(ctx, ctx.BlockHeader().ProposerAddress)
	if err != nil {
		return out, nil, err
	}
	utils, err := evmante.NewMonoDecoratorUtils(ctx, p.evm, &cfg.Params, &cfg.FeeMarketParams)
	if err != nil {
		return out, nil, err
	}
	header := &ethtypes.Header{GasLimit: antetypes.BlockGasLimit(ctx), BaseFee: utils.BaseFee,
		Number: big.NewInt(ctx.BlockHeight()), Time: uint64(ctx.BlockTime().Unix()), Difficulty: new(big.Int)}
	if err := txpool.ValidateTransaction(tx, header, utils.Signer, &txpool.ValidationOptions{
		Config:  chain,
		Accept:  1<<ethtypes.LegacyTxType | 1<<ethtypes.AccessListTxType | 1<<ethtypes.DynamicFeeTxType,
		MaxSize: uint64(p.options.MaxBytes), MinTip: new(big.Int),
	}); err != nil {
		return out, err, nil
	}
	if err := evmante.ValidateMsg(cfg.Params, tx); err != nil {
		return out, err, nil
	}
	msg, err := core.TransactionToMessage(tx, utils.Signer, utils.BaseFee)
	if err != nil {
		return out, err, nil
	}
	account := p.evm.GetAccount(ctx, msg.From)
	if err := evmante.VerifyAccountBalance(ctx, p.evm, p.accounts, account, msg.From, tx); err != nil {
		return out, err, nil
	}
	if err := evmante.CanTransfer(ctx, p.evm, *msg, utils.BaseFee, cfg.Params, utils.Rules.IsLondon); err != nil {
		return out, err, nil
	}
	acc := p.accounts.GetAccount(ctx, msg.From.Bytes())
	if acc == nil || acc.GetSequence() != tx.Nonce() || tx.Nonce() == math.MaxUint64 {
		return out, fmt.Errorf("invalid or exhausted sender nonce"), nil
	}
	fee := new(big.Int).Mul(new(big.Int).SetUint64(tx.Gas()), msg.GasPrice)
	if err := evmante.CheckGlobalFee(sdkmath.LegacyNewDecFromBigInt(fee), utils.GlobalMinGasPrice,
		sdkmath.LegacyNewDecFromBigInt(new(big.Int).SetUint64(tx.Gas()))); err != nil {
		return out, err, nil
	}
	fees, err := evmkeeper.VerifyFee(tx, evmtypes.GetEVMCoinDenom(), utils.BaseFee,
		utils.Rules.IsHomestead, utils.Rules.IsIstanbul, utils.Rules.IsShanghai, true)
	if err != nil {
		return out, err, nil
	}
	if block := ctx.BlockGasMeter(); block != nil && block.Limit() != 0 && block.GasRemaining() < tx.Gas() {
		return out, nil, ErrSignedTransactionBudget
	}
	return preparedSignedTransaction{sender: msg.From, price: msg.GasPrice, fees: fees,
		baseFeeEnabled: cfg.FeeMarketParams.IsBaseFeeEnabled(ctx.BlockHeight())}, nil, nil
}

func (p *SignedTransactionProcessor) applySignedTransaction(ctx sdk.Context, tx *ethtypes.Transaction) (*evmtypes.MsgEthereumTxResponse, error) {
	// ApplyTransaction resets its context gas meter. Give it an isolated meter so
	// an inner call cannot reset/refund gas already spent by the enclosing IBC tx.
	inner := evmexecution.BuildEvmExecutionCtx(ctx).
		WithGasMeter(evmtypes.NewInfiniteGasMeterWithLimit(tx.Gas()))
	previous := p.evm.GetTransientGasUsed(inner)
	p.evm.ResetTransientGasUsed(inner)
	response, err := p.evm.ApplyTransaction(inner, tx)
	// Preserve bookkeeping for other EVM messages in the enclosing SDK tx.
	p.evm.SetTransientGasUsed(inner, previous)
	return response, err
}
