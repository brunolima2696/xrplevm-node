package fabricevents

import (
	"context"
	"errors"
	"math"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	anteinterfaces "github.com/cosmos/evm/ante/interfaces"
	feemarkettypes "github.com/cosmos/evm/x/feemarket/types"
	"github.com/cosmos/evm/x/vm/statedb"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/holiman/uint256"
)

var signedConfigOnce sync.Once

// The KV-backed doubles below exercise the real ante validation helpers and SDK
// cache/commit semantics. ApplyTransaction itself is a test double, not a claim
// that a full node/EVM integration test has been run.
type signedTestAccounts struct {
	anteinterfaces.AccountKeeper
	key *storetypes.KVStoreKey
}

func (a signedTestAccounts) GetAccount(goCtx context.Context, addr sdk.AccAddress) sdk.AccountI {
	ctx := sdk.UnwrapSDKContext(goCtx)
	bz := ctx.KVStore(a.key).Get(append([]byte("nonce/"), addr...))
	if bz == nil {
		return nil
	}
	return authtypes.NewBaseAccount(addr, nil, 0, sdk.BigEndianToUint64(bz))
}
func (a signedTestAccounts) SetAccount(goCtx context.Context, acc sdk.AccountI) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	ctx.KVStore(a.key).Set(append([]byte("nonce/"), acc.GetAddress()...), sdk.Uint64ToBigEndian(acc.GetSequence()))
}
func (a signedTestAccounts) NewAccountWithAddress(_ context.Context, addr sdk.AccAddress) sdk.AccountI {
	return authtypes.NewBaseAccountWithAddress(addr)
}

type signedTestFees struct {
	anteinterfaces.FeeMarketKeeper
	key    *storetypes.KVStoreKey
	params feemarkettypes.Params
	fail   bool
}

func (f *signedTestFees) GetParams(sdk.Context) feemarkettypes.Params { return f.params }
func (f *signedTestFees) AddTransientGasWanted(ctx sdk.Context, gas uint64) (uint64, error) {
	store := ctx.KVStore(f.key)
	current := uint64(0)
	if bz := store.Get([]byte("gas_wanted")); bz != nil {
		current = sdk.BigEndianToUint64(bz)
	}
	store.Set([]byte("gas_wanted"), sdk.Uint64ToBigEndian(current+gas))
	if f.fail {
		return 0, errors.New("fee market unavailable")
	}
	return current + gas, nil
}

type signedTestEVM struct {
	anteinterfaces.EVMKeeper
	key            *storetypes.KVStoreKey
	accounts       signedTestAccounts
	fees           *signedTestFees
	params         evmtypes.Params
	mode           string
	calls          int
	contractSender bool
}

func (e *signedTestEVM) balance(ctx sdk.Context, addr common.Address) *big.Int {
	return new(big.Int).SetBytes(ctx.KVStore(e.key).Get(append([]byte("balance/"), addr.Bytes()...)))
}
func (e *signedTestEVM) setBalance(ctx sdk.Context, addr common.Address, balance *big.Int) {
	bz := balance.Bytes()
	if len(bz) == 0 {
		bz = []byte{0}
	}
	ctx.KVStore(e.key).Set(append([]byte("balance/"), addr.Bytes()...), bz)
}
func (e *signedTestEVM) GetAccount(ctx sdk.Context, addr common.Address) *statedb.Account {
	acc := e.accounts.GetAccount(ctx, addr.Bytes())
	if acc == nil {
		return nil
	}
	state := statedb.NewEmptyAccount()
	state.Nonce = acc.GetSequence()
	state.Balance = uint256.MustFromBig(e.balance(ctx, addr))
	if e.contractSender {
		state.CodeHash = common.HexToHash("0x01").Bytes()
	}
	return state
}
func (e *signedTestEVM) GetCode(sdk.Context, common.Hash) []byte { return []byte{0x00} }
func (e *signedTestEVM) EVMConfig(sdk.Context, sdk.ConsAddress) (*statedb.EVMConfig, error) {
	return &statedb.EVMConfig{Params: e.params, FeeMarketParams: e.fees.params, BaseFee: big.NewInt(1)}, nil
}
func (e *signedTestEVM) GetTxIndexTransient(sdk.Context) uint64 { return 0 }
func (e *signedTestEVM) GetTransientGasUsed(ctx sdk.Context) uint64 {
	if bz := ctx.KVStore(e.key).Get([]byte("evm_gas")); bz != nil {
		return sdk.BigEndianToUint64(bz)
	}
	return 0
}
func (e *signedTestEVM) SetTransientGasUsed(ctx sdk.Context, gas uint64) {
	ctx.KVStore(e.key).Set([]byte("evm_gas"), sdk.Uint64ToBigEndian(gas))
}
func (e *signedTestEVM) ResetTransientGasUsed(ctx sdk.Context) { e.SetTransientGasUsed(ctx, 0) }
func (e *signedTestEVM) DeductTxCostsFromUserBalance(ctx sdk.Context, fees sdk.Coins, from common.Address) error {
	amount := fees.AmountOf(evmtypes.GetEVMCoinDenom()).BigInt()
	balance := e.balance(ctx, from)
	if balance.Cmp(amount) < 0 {
		return errors.New("not enough fee balance")
	}
	e.setBalance(ctx, from, new(big.Int).Sub(balance, amount))
	ctx.KVStore(e.key).Set([]byte("charged"), amount.Bytes())
	return nil
}
func (e *signedTestEVM) ApplyTransaction(ctx sdk.Context, tx *ethtypes.Transaction) (*evmtypes.MsgEthereumTxResponse, error) {
	e.calls++
	if ctx.GasMeter().GasConsumed() != 0 {
		panic("inner gas meter was not isolated")
	}
	ctx.GasMeter().ConsumeGas(123, "simulated engine work")
	e.SetTransientGasUsed(ctx, 23000)
	ctx.KVStore(e.key).Set([]byte("engine_write"), []byte{1})
	if e.mode == "error" {
		return nil, errors.New("engine internal failure")
	}
	if e.mode == "panic" {
		panic("engine panic")
	}
	if e.mode == "nil" {
		return nil, nil
	}
	if e.mode == "invalid" {
		return &evmtypes.MsgEthereumTxResponse{Hash: tx.Hash().Hex(), GasUsed: tx.Gas() + 1}, nil
	}
	msg, err := core.TransactionToMessage(tx, ethtypes.LatestSignerForChainID(tx.ChainId()), big.NewInt(1))
	if err != nil {
		return nil, err
	}
	// Model the fee refund performed by the real ApplyTransaction. This deliberately
	// does not reimplement a VM. Network tests must exercise the actual EVM keeper.
	gasUsed := uint64(23000)
	refund := new(big.Int).Mul(new(big.Int).SetUint64(tx.Gas()-gasUsed), msg.GasPrice)
	e.setBalance(ctx, msg.From, new(big.Int).Add(e.balance(ctx, msg.From), refund))
	response := &evmtypes.MsgEthereumTxResponse{Hash: tx.Hash().Hex(), GasUsed: gasUsed}
	if e.mode == "revert" {
		response.VmError = "execution reverted"
	} else {
		e.setBalance(ctx, msg.From, new(big.Int).Sub(e.balance(ctx, msg.From), tx.Value()))
		if tx.To() != nil {
			e.setBalance(ctx, *tx.To(), new(big.Int).Add(e.balance(ctx, *tx.To()), tx.Value()))
		}
	}
	ctx.EventManager().EmitEvent(sdk.NewEvent("signed_test_execution"))
	return response, nil
}

type signedFixture struct {
	ctx       sdk.Context
	key       *storetypes.KVStoreKey
	engine    *signedTestEVM
	accounts  signedTestAccounts
	fees      *signedTestFees
	processor *SignedTransactionProcessor
	sender    common.Address
	to        common.Address
	id        string
}

func newSignedFixture(t *testing.T) *signedFixture {
	t.Helper()
	signedConfigOnce.Do(func() {
		if err := evmtypes.SetChainConfig(evmtypes.DefaultChainConfig(1450001)); err != nil {
			t.Fatal(err)
		}
		evmtypes.SetDefaultEvmCoinInfo(evmtypes.EvmCoinInfo{Denom: "axrp", ExtendedDenom: "axrp", DisplayDenom: "XRP", Decimals: 18})
	})
	key := storetypes.NewKVStoreKey("signed_test")
	ctx := testutil.DefaultContext(key, storetypes.NewTransientStoreKey("signed_transient")).
		WithBlockHeight(10).WithBlockTime(time.Unix(1800000000, 0)).
		WithExecMode(sdk.ExecModeFinalize).
		WithConsensusParams(cmtproto.ConsensusParams{Block: &cmtproto.BlockParams{MaxGas: 10000000}}).
		WithGasMeter(storetypes.NewGasMeter(2000000)).
		WithBlockGasMeter(storetypes.NewGasMeter(10000000))
	accounts := signedTestAccounts{key: key}
	fees := &signedTestFees{key: key, params: feemarkettypes.DefaultParams()}
	fees.params.BaseFee = sdkmath.LegacyNewDec(1)
	engine := &signedTestEVM{key: key, accounts: accounts, fees: fees, params: evmtypes.DefaultParams()}
	processor, err := newSignedTransactionProcessor(engine, accounts, fees, SignedTransactionOptions{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	keypair, _ := crypto.HexToECDSA(strings.Repeat("0", 63) + "1")
	sender := crypto.PubkeyToAddress(keypair.PublicKey)
	accounts.SetAccount(ctx, authtypes.NewBaseAccountWithAddress(sender.Bytes()))
	engine.setBalance(ctx, sender, big.NewInt(5000000))
	engine.SetTransientGasUsed(ctx, 999)
	ctx.GasMeter().ConsumeGas(7000, "already spent by outer transaction")
	return &signedFixture{ctx: ctx, key: key, engine: engine, accounts: accounts, fees: fees, processor: processor,
		sender: sender, to: common.HexToAddress("0x2222222222222222222222222222222222222222"), id: "0x" + strings.Repeat("11", 32)}
}
func (f *signedFixture) raw(t *testing.T, typ uint8, nonce uint64, chain int64, data []byte, gas uint64, price int64) []byte {
	t.Helper()
	var inner ethtypes.TxData
	switch typ {
	case ethtypes.LegacyTxType:
		inner = &ethtypes.LegacyTx{Nonce: nonce, To: &f.to, Value: big.NewInt(1000), Gas: gas, GasPrice: big.NewInt(price), Data: data}
	case ethtypes.AccessListTxType:
		inner = &ethtypes.AccessListTx{ChainID: big.NewInt(chain), Nonce: nonce, To: &f.to, Value: big.NewInt(1000), Gas: gas, GasPrice: big.NewInt(price), Data: data}
	default:
		inner = &ethtypes.DynamicFeeTx{ChainID: big.NewInt(chain), Nonce: nonce, To: &f.to, Value: big.NewInt(1000), Gas: gas, GasFeeCap: big.NewInt(price), GasTipCap: big.NewInt(1), Data: data}
	}
	keypair, _ := crypto.HexToECDSA(strings.Repeat("0", 63) + "1")
	tx, err := ethtypes.SignTx(ethtypes.NewTx(inner), ethtypes.LatestSignerForChainID(big.NewInt(chain)), keypair)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := tx.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func (f *signedFixture) nonce() uint64 {
	return f.accounts.GetAccount(f.ctx, f.sender.Bytes()).GetSequence()
}
func (f *signedFixture) assertUnchanged(t *testing.T) {
	t.Helper()
	if f.engine.balance(f.ctx, f.sender).Cmp(big.NewInt(5000000)) != 0 || f.nonce() != 0 || f.engine.balance(f.ctx, f.to).Sign() != 0 {
		t.Fatal("rejected/aborted execution changed balances or nonce")
	}
	for _, key := range []string{"charged", "engine_write", "gas_wanted"} {
		if f.ctx.KVStore(f.key).Has([]byte(key)) {
			t.Fatalf("cached %s leaked", key)
		}
	}
	if f.engine.GetTransientGasUsed(f.ctx) != 999 {
		t.Fatal("transient accounting changed")
	}
	if len(f.ctx.EventManager().Events()) != 0 {
		t.Fatal("cached events leaked")
	}
}

func TestSignedTransactionDisabledAndBounded(t *testing.T) {
	f := newSignedFixture(t)
	f.processor.options.Enabled = false
	before := f.ctx.GasMeter().GasConsumed()
	_, err := f.processor.Process(f.ctx, []byte{1}, "")
	if !errors.Is(err, ErrSignedTransactionsDisabled) || f.ctx.GasMeter().GasConsumed() != before {
		t.Fatal("disabled processor executed work")
	}
	f.assertUnchanged(t)
	for _, options := range []SignedTransactionOptions{{MaxGas: 20000}, {MaxGas: 3000001}, {MaxBytes: -1}, {MaxBytes: 16385}} {
		if _, err := newSignedTransactionProcessor(nil, nil, nil, options); err == nil {
			t.Fatal("invalid bounds accepted")
		}
	}
	if _, err := NewSignedTransactionProcessor(nil, nil, nil, SignedTransactionOptions{}); err == nil {
		t.Fatal("nil keeper accepted")
	}
}

func TestSignedTransactionRejectsWithoutMutations(t *testing.T) {
	for _, name := range []string{"malformed", "oversized", "wrong-chain", "unsigned", "bad-signature", "nonce-gap", "nonce-exhausted", "insufficient-balance", "low-fee", "global-min-fee", "intrinsic-gas", "gas-cap", "order-mismatch", "bad-order", "contract-sender", "creation-disabled", "call-disabled"} {
		t.Run(name, func(t *testing.T) {
			f := newSignedFixture(t)
			raw := f.raw(t, 2, 0, 1450001, common.HexToHash(f.id).Bytes(), 60000, 3)
			id := f.id
			switch name {
			case "malformed":
				raw = []byte{0xff}
			case "oversized":
				raw = make([]byte, 4097)
			case "wrong-chain":
				raw = f.raw(t, 2, 0, 2, nil, 60000, 3)
			case "unsigned":
				raw, _ = ethtypes.NewTx(&ethtypes.LegacyTx{To: &f.to, Gas: 60000, GasPrice: big.NewInt(3), Value: big.NewInt(1)}).MarshalBinary()
			case "bad-signature":
				raw, _ = ethtypes.NewTx(&ethtypes.LegacyTx{To: &f.to, Gas: 60000, GasPrice: big.NewInt(3), V: big.NewInt(2900037), R: big.NewInt(0), S: big.NewInt(0)}).MarshalBinary()
			case "nonce-gap":
				raw = f.raw(t, 2, 1, 1450001, nil, 60000, 3)
			case "nonce-exhausted":
				raw = f.raw(t, 2, math.MaxUint64, 1450001, nil, 60000, 3)
			case "insufficient-balance":
				raw = f.raw(t, 2, 0, 1450001, nil, 60000, 1000)
			case "low-fee":
				raw = f.raw(t, 0, 0, 1450001, nil, 60000, 0)
			case "global-min-fee":
				f.fees.params.MinGasPrice = sdkmath.LegacyNewDec(100)
			case "intrinsic-gas":
				raw = f.raw(t, 2, 0, 1450001, nil, 1, 3)
			case "gas-cap":
				raw = f.raw(t, 2, 0, 1450001, nil, 1000001, 3)
			case "order-mismatch":
				id = "0x" + strings.Repeat("22", 32)
			case "bad-order":
				id = "0xnothex"
			case "contract-sender":
				f.engine.contractSender = true
			case "creation-disabled":
				f.engine.params.AccessControl.Create.AccessType = evmtypes.AccessTypeRestricted
				tx, _ := decodeSignedTransaction(raw)
				keypair, _ := crypto.HexToECDSA(strings.Repeat("0", 63) + "1")
				creation, _ := ethtypes.SignTx(ethtypes.NewTx(&ethtypes.LegacyTx{Gas: 100000, GasPrice: big.NewInt(3), Data: tx.Data()}), ethtypes.LatestSignerForChainID(big.NewInt(1450001)), keypair)
				raw, _ = creation.MarshalBinary()
			case "call-disabled":
				f.engine.params.AccessControl.Call.AccessType = evmtypes.AccessTypeRestricted
			}
			result, err := f.processor.Process(f.ctx, raw, id)
			if err != nil || result.Status != "rejected" || result.Reason == "" || f.engine.calls != 0 {
				t.Fatalf("unexpected rejection: %+v %v", result, err)
			}
			if (name == "malformed" || name == "oversized") && result.Hash != "" {
				t.Fatal("invalid raw bytes have hash")
			}
			f.assertUnchanged(t)
		})
	}
}

func TestSignedTransactionSuccessFeesNonceAndReplay(t *testing.T) {
	for _, typ := range []uint8{0, 1, 2} {
		t.Run(string(rune('0'+typ)), func(t *testing.T) {
			f := newSignedFixture(t)
			raw := f.raw(t, typ, 0, 1450001, common.HexToHash(f.id).Bytes(), 60000, 3)
			beforeGas := f.ctx.GasMeter().GasConsumed()
			result, err := f.processor.Process(f.ctx, raw, f.id)
			if err != nil || result.Status != "success" || f.nonce() != 1 || result.From != f.sender.Hex() {
				t.Fatalf("%+v %v", result, err)
			}
			tx, _ := decodeSignedTransaction(raw)
			msg, _ := core.TransactionToMessage(tx, ethtypes.LatestSignerForChainID(tx.ChainId()), big.NewInt(1))
			expected := new(big.Int).Sub(big.NewInt(5000000), new(big.Int).Add(big.NewInt(1000), new(big.Int).Mul(big.NewInt(23000), msg.GasPrice)))
			if result.Hash != tx.Hash().Hex() || f.engine.balance(f.ctx, f.sender).Cmp(expected) != 0 || f.engine.balance(f.ctx, f.to).Cmp(big.NewInt(1000)) != 0 {
				t.Fatal("incorrect signed payment/fee/hash")
			}
			if result.Input != f.id || result.GasUsed != 23000 || result.Height != 10 || f.engine.GetTransientGasUsed(f.ctx) != 999 {
				t.Fatal("execution report/accounting mismatch")
			}
			if f.ctx.GasMeter().GasConsumed() < beforeGas+23000 {
				t.Fatal("outer gas meter was reset or not charged")
			}
			result, err = f.processor.Process(f.ctx, raw, f.id)
			if err != nil || result.Status != "rejected" || f.engine.calls != 1 || f.nonce() != 1 || f.engine.balance(f.ctx, f.sender).Cmp(expected) != 0 {
				t.Fatal("replay executed twice")
			}
		})
	}
}

func TestSignedTransactionRevertKeepsFeesAndNonce(t *testing.T) {
	f := newSignedFixture(t)
	f.engine.mode = "revert"
	raw := f.raw(t, 2, 0, 1450001, nil, 60000, 3)
	result, err := f.processor.Process(f.ctx, raw, "")
	if err != nil || result.Status != "reverted" || f.nonce() != 1 || f.engine.balance(f.ctx, f.to).Sign() != 0 || f.engine.balance(f.ctx, f.sender).Cmp(big.NewInt(5000000-23000*2)) != 0 {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestSignedTransactionEngineFailuresRollback(t *testing.T) {
	for _, mode := range []string{"error", "nil", "invalid", "panic"} {
		t.Run(mode, func(t *testing.T) {
			f := newSignedFixture(t)
			f.engine.mode = mode
			raw := f.raw(t, 2, 0, 1450001, nil, 60000, 3)
			func() {
				if mode == "panic" {
					defer func() {
						if recover() == nil {
							t.Error("engine panic swallowed")
						}
					}()
				}
				_, err := f.processor.Process(f.ctx, raw, "")
				if mode != "panic" && err == nil {
					t.Error("engine failure accepted")
				}
			}()
			f.assertUnchanged(t)
		})
	}
	f := newSignedFixture(t)
	f.fees.fail = true
	_, err := f.processor.Process(f.ctx, f.raw(t, 2, 0, 1450001, nil, 60000, 3), "")
	if err == nil {
		t.Fatal("fee market failure ignored")
	}
	f.assertUnchanged(t)
}

func TestSignedTransactionOuterRollbackAndGasBudgets(t *testing.T) {
	f := newSignedFixture(t)
	raw := f.raw(t, 2, 0, 1450001, nil, 60000, 3)
	outer, _ := f.ctx.CacheContext()
	result, err := f.processor.Process(outer, raw, "")
	if err != nil || result.Status != "success" {
		t.Fatalf("%+v %v", result, err)
	}
	// Simulate the enclosing IBC transaction failing: never call its write closure.
	f.assertUnchanged(t)
	f.ctx = f.ctx.WithGasMeter(storetypes.NewGasMeter(100000))
	_, err = f.processor.Process(f.ctx, raw, "")
	if !errors.Is(err, ErrSignedTransactionBudget) {
		t.Fatalf("expected retriable budget error, got %v", err)
	}
	f.assertUnchanged(t)
	blockMeter := storetypes.NewGasMeter(10000000)
	blockMeter.ConsumeGas(9950000, "previous transactions in block")
	f.ctx = f.ctx.WithGasMeter(storetypes.NewGasMeter(2000000)).WithBlockGasMeter(blockMeter)
	_, err = f.processor.Process(f.ctx, raw, "")
	if !errors.Is(err, ErrSignedTransactionBudget) {
		t.Fatalf("expected block budget error, got %v", err)
	}
	f.assertUnchanged(t)
	f.ctx = f.ctx.WithExecMode(sdk.ExecModeCheck)
	if _, err = f.processor.Process(f.ctx, raw, ""); err == nil {
		t.Fatal("execution during CheckTx accepted")
	}
}
