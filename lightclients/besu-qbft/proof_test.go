package qbft

import (
	"bytes"
	"math/big"
	"testing"
	"time"

	"cosmossdk.io/store/dbadapter"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	clienttypes "github.com/cosmos/ibc-go/v10/modules/core/02-client/types"
	commitmenttypes "github.com/cosmos/ibc-go/v10/modules/core/23-commitment/types/v2"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/stretchr/testify/require"
)

// Build a real single-leaf MPT and the RLP proof format emitted by YUI.
func leafProof(t *testing.T, key, value []byte) ([]byte, []byte) {
	t.Helper()
	leaf := [][]byte{append([]byte{0x20}, key...), value}
	node, err := rlp.EncodeToBytes(leaf)
	require.NoError(t, err)
	proof, err := rlp.EncodeToBytes([][][]byte{leaf})
	require.NoError(t, err)
	return crypto.Keccak256(node), proof
}

func TestSignedHeaderAndStorageProof(t *testing.T) {
	registry := codectypes.NewInterfaceRegistry()
	AppModuleBasic{}.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)
	store := dbadapter.Store{DB: dbm.NewMemDB()}
	ctx := sdk.Context{}.WithBlockTime(time.Unix(110, 0)).WithBlockHeight(5).WithChainID("xrplevm_1450001-1")
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	validator := crypto.PubkeyToAddress(key.PublicKey)
	address := bytes.Repeat([]byte{1}, 20)
	height := clienttypes.NewHeight(0, 10)
	cs := &ClientState{IbcStoreAddress: address, LatestHeight: height, TrustingPeriod: 100}
	cons := &ConsensusState{Timestamp: 100, Root: bytes.Repeat([]byte{2}, 32), Validators: [][]byte{validator.Bytes()}}
	require.NoError(t, cs.Initialize(ctx, cdc, store, cons))

	path := []byte("connections/connection-0")
	value := []byte("connection commitment")
	storageKey, err := storageTrieKey(path)
	require.NoError(t, err)
	storageValue, err := rlp.EncodeToBytes(crypto.Keccak256(value))
	require.NoError(t, err)
	storageRoot, storageProof := leafProof(t, storageKey, storageValue)
	account, err := rlp.EncodeToBytes(gethAccount{Balance: big.NewInt(0), Root: common.BytesToHash(storageRoot), CodeHash: bytes.Repeat([]byte{4}, 32)})
	require.NoError(t, err)
	stateRoot, accountProof := leafProof(t, crypto.Keccak256(address), account)
	extra, err := rlp.EncodeToBytes(ExtraData{Vanity: make([]byte, 32), Validators: []common.Address{validator}, Vote: rlp.RawValue{0xc0}, Round: []byte{0, 0, 0, 0}})
	require.NoError(t, err)
	block := &gethtypes.Header{Number: big.NewInt(11), Time: 101, Root: common.BytesToHash(stateRoot), Extra: extra, Difficulty: big.NewInt(1)}
	raw, err := rlp.EncodeToBytes(block)
	require.NoError(t, err)
	seal, err := crypto.Sign(crypto.Keccak256(raw), key)
	require.NoError(t, err)
	header := &Header{BesuHeaderRlp: raw, Seals: [][]byte{seal}, TrustedHeight: height, AccountStateProof: accountProof}
	require.NoError(t, cs.VerifyClientMessage(ctx, cdc, store, header))
	cs.UpdateState(ctx, cdc, store, header)
	newHeight := clienttypes.NewHeight(0, 11)
	require.Equal(t, newHeight, cs.LatestHeight)
	proofPath := commitmenttypes.MerklePath{KeyPath: [][]byte{[]byte("ibc"), path}}
	require.NoError(t, cs.VerifyMembership(ctx, store, cdc, newHeight, 0, 0, storageProof, proofPath, value))
	require.Error(t, cs.VerifyMembership(ctx, store, cdc, newHeight, 0, 0, storageProof, proofPath, []byte("forged")))
	require.Error(t, cs.VerifyNonMembership(ctx, store, cdc, newHeight, 0, 0, storageProof, proofPath))
	absentPath := commitmenttypes.MerklePath{KeyPath: [][]byte{[]byte("ibc"), []byte("connections/connection-999")}}
	require.NoError(t, cs.VerifyNonMembership(ctx, store, cdc, newHeight, 0, 0, storageProof, absentPath))

	// A fully signed header from a different validator set must not be accepted.
	attacker, err := crypto.GenerateKey()
	require.NoError(t, err)
	extra, err = rlp.EncodeToBytes(ExtraData{Vanity: make([]byte, 32), Validators: []common.Address{crypto.PubkeyToAddress(attacker.PublicKey)}, Vote: rlp.RawValue{0xc0}, Round: []byte{0, 0, 0, 0}})
	require.NoError(t, err)
	block.Extra = extra
	raw, err = rlp.EncodeToBytes(block)
	require.NoError(t, err)
	seal, err = crypto.Sign(crypto.Keccak256(raw), attacker)
	require.NoError(t, err)
	header.BesuHeaderRlp = raw
	header.Seals = [][]byte{seal}
	require.ErrorContains(t, cs.VerifyClientMessage(ctx, cdc, store, header), "trusted validators")
}
