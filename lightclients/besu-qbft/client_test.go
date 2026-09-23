package qbft

import (
	"bytes"
	"testing"
	"time"

	"cosmossdk.io/store/dbadapter"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	clienttypes "github.com/cosmos/ibc-go/v10/modules/core/02-client/types"
	commitmenttypes "github.com/cosmos/ibc-go/v10/modules/core/23-commitment/types/v2"
	"github.com/cosmos/ibc-go/v10/modules/core/exported"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

func TestWireTypesAndStore(t *testing.T) {
	registry := codectypes.NewInterfaceRegistry()
	AppModuleBasic{}.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)
	height := clienttypes.NewHeight(0, 10)
	cs := &ClientState{IbcStoreAddress: bytes.Repeat([]byte{1}, 20), LatestHeight: height, TrustingPeriod: 100}
	cons := &ConsensusState{Root: bytes.Repeat([]byte{2}, 32), Timestamp: 100, Validators: [][]byte{bytes.Repeat([]byte{3}, 20)}}
	require.NoError(t, cs.Validate())
	require.NoError(t, cons.ValidateBasic())
	for _, tc := range []struct {
		value exported.ClientState
		url   string
	}{{cs, "/ibc.lightclients.qbft.v1.ClientState"}} {
		packed, err := codectypes.NewAnyWithValue(tc.value)
		require.NoError(t, err)
		require.Equal(t, tc.url, packed.TypeUrl)
		var decoded exported.ClientState
		require.NoError(t, registry.UnpackAny(packed, &decoded))
	}
	store := dbadapter.Store{DB: dbm.NewMemDB()}
	ctx := sdk.Context{}.WithBlockTime(time.Unix(110, 0)).WithBlockHeight(5).WithChainID("xrplevm_1450001-1")
	require.NoError(t, cs.Initialize(ctx, cdc, store, cons))
	restored, ok := getClientState(store, cdc)
	require.True(t, ok)
	require.Equal(t, height, restored.LatestHeight)
	require.Equal(t, exported.Active, cs.Status(ctx, store, cdc))
	require.Equal(t, exported.Expired, cs.Status(ctx.WithBlockTime(time.Unix(201, 0)), store, cdc))
	stamp, err := cs.GetTimestampAtHeight(ctx, store, cdc, height)
	require.NoError(t, err)
	require.Equal(t, uint64(100000000000), stamp)
	require.ErrorIs(t, verifyDelayPeriodPassed(ctx, store, height, 1, 0), ErrDelayPeriodNotPassed)
	require.NoError(t, verifyDelayPeriodPassed(ctx.WithBlockTime(time.Unix(111, 0)), store, height, 1, 0))
}

func TestOrderedSeals(t *testing.T) {
	hash := crypto.Keccak256([]byte("header"))
	var validators []common.Address
	var seals [][]byte
	for i := 0; i < 4; i++ {
		key, err := crypto.GenerateKey()
		require.NoError(t, err)
		validators = append(validators, crypto.PubkeyToAddress(key.PublicKey))
		seal, err := crypto.Sign(hash, key)
		require.NoError(t, err)
		seals = append(seals, seal)
	}
	require.NoError(t, verifyQBFTOrderedSeals(hash, validators, seals))
	seals[3] = nil
	require.NoError(t, verifyQBFTOrderedSeals(hash, validators, seals))
	seals[2] = nil
	require.Error(t, verifyQBFTOrderedSeals(hash, validators, seals))
	require.Error(t, verifyQBFTOrderedSeals(crypto.Keccak256([]byte("forged")), validators, seals))
	require.Error(t, verifyQBFTOrderedSeals(hash, []common.Address{validators[0], validators[0]}, [][]byte{seals[0], seals[0]}))
}

func TestV10PathsAndMalformedProofs(t *testing.T) {
	path := commitmenttypes.MerklePath{KeyPath: [][]byte{[]byte("ibc"), []byte("connections/connection-0")}}
	got, err := ics24Path(path)
	require.NoError(t, err)
	require.Equal(t, []byte("connections/connection-0"), got)
	_, err = ics24Path(commitmenttypes.MerklePath{})
	require.Error(t, err)
	_, _, err = verifyMPTMembership(bytes.Repeat([]byte{1}, 32), got, []byte{1, 2, 3})
	require.Error(t, err)
	require.Error(t, (&Header{}).ValidateBasic())
}
