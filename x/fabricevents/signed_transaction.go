package fabricevents

// This processor is callable only by the authenticated version-3 IBC route.
// There is no message server, precompile or RPC broadcast entry for it.

import (
	"bytes"
	"errors"
	"fmt"
	"math"

	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	anteinterfaces "github.com/cosmos/evm/ante/interfaces"
	evmkeeper "github.com/cosmos/evm/x/vm/keeper"
	"github.com/cosmos/evm/x/vm/statedb"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

var (
	ErrSignedTransactionsDisabled = errors.New("signed transaction processor is disabled")
	ErrSignedTransactionBudget    = errors.New("insufficient outer transaction gas budget")
)

// Explicit application opt-in. The IBC route additionally requires governance-
// authorized Fabric clients and applies the on-chain Config.GasLimit cap.
type SignedTransactionOptions struct {
	Enabled  bool
	MaxGas   uint64
	MaxBytes int
}

// SignedTransactionResult is an execution report, NOT an eth RPC receipt or an
// assertion that the inner transaction is independently indexed in an eth block.
// A caller must commit its enclosing SDK transaction before reporting finality.
type SignedTransactionResult struct {
	OrderID           string          `json:"id_pedido,omitempty"`
	Status            string          `json:"status"` // rejected, success, reverted
	Hash              string          `json:"hash,omitempty"`
	Reason            string          `json:"reason,omitempty"`
	From              string          `json:"from,omitempty"`
	To                *common.Address `json:"to,omitempty"`
	Input             string          `json:"input,omitempty"`
	RawTransaction    string          `json:"raw_transaction,omitempty"`
	GasUsed           uint64          `json:"gas_used,omitempty"`
	EffectiveGasPrice string          `json:"effective_gas_price,omitempty"`
	Height            int64           `json:"height,omitempty"`
	BlockHash         string          `json:"block_hash,omitempty"`
	ReturnData        string          `json:"return_data,omitempty"`
	Logs              []*ethtypes.Log `json:"logs,omitempty"`
}

type signedTransactionEVM interface {
	anteinterfaces.EVMKeeper
	EVMConfig(sdk.Context, sdk.ConsAddress) (*statedb.EVMConfig, error)
	ApplyTransaction(sdk.Context, *ethtypes.Transaction) (*evmtypes.MsgEthereumTxResponse, error)
	GetTransientGasUsed(sdk.Context) uint64
	SetTransientGasUsed(sdk.Context, uint64)
}

type SignedTransactionProcessor struct {
	evm      signedTransactionEVM
	accounts anteinterfaces.AccountKeeper
	fees     anteinterfaces.FeeMarketKeeper
	options  SignedTransactionOptions
}

func NewSignedTransactionProcessor(evm *evmkeeper.Keeper, accounts anteinterfaces.AccountKeeper, fees anteinterfaces.FeeMarketKeeper, options SignedTransactionOptions) (*SignedTransactionProcessor, error) {
	if evm == nil || accounts == nil || fees == nil {
		return nil, fmt.Errorf("signed transaction processor requires EVM, account and fee-market keepers")
	}
	return newSignedTransactionProcessor(evm, accounts, fees, options)
}

func newSignedTransactionProcessor(evm signedTransactionEVM, accounts anteinterfaces.AccountKeeper, fees anteinterfaces.FeeMarketKeeper, options SignedTransactionOptions) (*SignedTransactionProcessor, error) {
	if options.MaxGas == 0 {
		options.MaxGas = 1000000
	}
	if options.MaxBytes == 0 {
		options.MaxBytes = 4096
	}
	if options.MaxGas > 3000000 || options.MaxGas < 21000 || options.MaxBytes > 16384 || options.MaxBytes < 1 {
		return nil, fmt.Errorf("signed transaction bounds must be 21000..3000000 gas and 1..16384 bytes")
	}
	return &SignedTransactionProcessor{evm: evm, accounts: accounts, fees: fees, options: options}, nil
}

// Process accepts canonical raw signed Ethereum bytes, NOT an unsigned core.Message.
// Validation rejection does not change Alice's state. EVM revert consumes nonce
// and gas, while reverting value/storage/log changes via ApplyTransaction.
// Non-nil error is an infrastructure/budget failure: do not acknowledge success.
// There is no mempool, network call, or direct balance manipulation here.
func (p *SignedTransactionProcessor) Process(ctx sdk.Context, raw []byte, orderID string) (SignedTransactionResult, error) {
	result := SignedTransactionResult{OrderID: orderID, Status: "rejected"}
	if !p.options.Enabled {
		return result, ErrSignedTransactionsDisabled
	}
	if ctx.IsCheckTx() || ctx.IsReCheckTx() {
		return result, fmt.Errorf("signed transaction execution is deliver-only")
	}
	if len(raw) == 0 || len(raw) > p.options.MaxBytes {
		result.Reason = "raw transaction size exceeds configured bounds"
		return result, nil
	}
	// This work is charged to the outer Cosmos/IBC transaction, independently of
	// the inner Ethereum fees. Never reset or refund the relayer's gas meter.
	ctx.GasMeter().ConsumeGas(uint64(5000+10*len(raw)), "signed transaction decode/signature budget")
	tx, err := decodeSignedTransaction(raw)
	if err != nil {
		result.Reason = err.Error()
		return result, nil
	}
	result.Hash = tx.Hash().Hex()
	result.RawTransaction = hexutil.Encode(raw)
	result.Input = hexutil.Encode(tx.Data())
	result.To = tx.To()
	if err := validateSignedOrderBinding(tx, orderID); err != nil {
		result.Reason = err.Error()
		return result, nil
	}
	if tx.Gas() > p.options.MaxGas {
		result.Reason = "signed gas limit exceeds processor cap"
		return result, nil
	}
	// Reserve enough gas for a worst-case EVM execution and returning/storing its
	// result. A small relayer gas limit must not turn a valid payment into a
	// permanently rejected order. Nothing is committed on a budget error.
	if meter := ctx.GasMeter(); meter.Limit() != 0 && meter.GasRemaining() < tx.Gas()+50000 {
		return result, ErrSignedTransactionBudget
	}

	work, commit := ctx.CacheContext()
	work = work.WithGasMeter(storetypes.NewInfiniteGasMeter())
	prepared, rejection, err := p.prepareSignedTransaction(work, tx)
	if err != nil {
		return result, err
	}
	if rejection != nil {
		result.Reason = rejection.Error()
		return result, nil
	}
	result.From = prepared.sender.Hex()
	result.EffectiveGasPrice = prepared.price.String()

	// All checks have passed. Fees and nonce are still in the disposable cache.
	// An internal Go error discards them; a VM revert is a valid included attempt.
	if err := p.evm.DeductTxCostsFromUserBalance(work, prepared.fees, prepared.sender); err != nil {
		result.Reason = err.Error()
		return result, nil
	}
	account := p.accounts.GetAccount(work, prepared.sender.Bytes())
	if account == nil {
		return result, fmt.Errorf("validated Ethereum sender account disappeared")
	}
	if err := incrementSignedNonce(work, p.accounts, account, tx.Nonce()); err != nil {
		result.Reason = err.Error()
		return result, nil
	}
	if prepared.baseFeeEnabled {
		if _, err := p.fees.AddTransientGasWanted(work, tx.Gas()); err != nil {
			return result, err
		}
	}

	response, err := p.applySignedTransaction(work, tx)
	if err != nil {
		// Charge bounded attempted execution to the outer meter, but do not keep
		// inner fees, nonce, value, events, or transient writes on engine failure.
		ctx.GasMeter().ConsumeGas(tx.Gas(), "failed inner EVM execution")
		return result, err
	}
	if response == nil || response.GasUsed > tx.Gas() || response.Hash != tx.Hash().Hex() {
		ctx.GasMeter().ConsumeGas(tx.Gas(), "inconsistent inner EVM execution")
		return result, fmt.Errorf("inconsistent signed EVM execution response")
	}
	ctx.GasMeter().ConsumeGas(response.GasUsed, "inner signed EVM execution")
	result.Status = "success"
	if response.Failed() {
		result.Status, result.Reason = "reverted", response.VmError
	}
	result.GasUsed = response.GasUsed
	result.ReturnData = hexutil.Encode(response.Ret)
	result.Logs = evmtypes.LogsToEthereum(response.Logs)
	result.Height = ctx.BlockHeight()
	if hash := ctx.HeaderHash(); len(hash) != 0 {
		result.BlockHash = common.BytesToHash(hash).Hex()
	}
	commit()
	return result, nil
}

func decodeSignedTransaction(raw []byte) (*ethtypes.Transaction, error) {
	tx := new(ethtypes.Transaction)
	if err := tx.UnmarshalBinary(raw); err != nil {
		return nil, fmt.Errorf("cannot decode raw Ethereum transaction")
	}
	canonical, err := tx.MarshalBinary()
	if err != nil || !bytes.Equal(canonical, raw) {
		return nil, fmt.Errorf("noncanonical raw Ethereum transaction")
	}
	return tx, nil
}

func validateSignedOrderBinding(tx *ethtypes.Transaction, id string) error {
	if id == "" {
		return nil
	}
	if len(id) != 66 {
		return fmt.Errorf("id_pedido must be a nonzero lowercase bytes32")
	}
	idBytes, err := hexutil.Decode(id)
	if err != nil || len(idBytes) != 32 || hexutil.Encode(idBytes) != id || common.BytesToHash(idBytes) == (common.Hash{}) {
		return fmt.Errorf("id_pedido must be a nonzero lowercase bytes32")
	}
	// Optional binding for a plain payment. Empty data means the metadata is NOT
	// covered by the Ethereum signature. Other calldata is forwarded unchanged.
	if len(tx.Data()) == 32 && !bytes.Equal(tx.Data(), idBytes) {
		return fmt.Errorf("signed data differs from id_pedido")
	}
	return nil
}

func incrementSignedNonce(ctx sdk.Context, accounts anteinterfaces.AccountKeeper, account sdk.AccountI, nonce uint64) error {
	if account.GetSequence() != nonce || nonce == math.MaxUint64 {
		return fmt.Errorf("invalid or exhausted sender nonce")
	}
	if err := account.SetSequence(nonce + 1); err != nil {
		return err
	}
	accounts.SetAccount(ctx, account)
	return nil
}
