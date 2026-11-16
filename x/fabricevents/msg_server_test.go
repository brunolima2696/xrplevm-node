package fabricevents

import (
	"bytes"
	"encoding/json"
	"testing"

	storetypes "cosmossdk.io/store/types"
	"cosmossdk.io/x/tx/signing"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/address"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/gogoproto/proto"
)

func TestUpdateConfigRequiresGovernanceAndPreservesReplay(t *testing.T) {
	key := storetypes.NewKVStoreKey(ModuleName)
	ctx := testutil.DefaultContext(key, storetypes.NewTransientStoreKey("test"))
	k := NewKeeper(key, nil)
	initial := Config{AllowedClientIDs: []string{"fabric-msp-0"}, GasLimit: 1000000}
	k.SetConfig(ctx, initial)
	ctx.KVStore(key).Set([]byte("event/existing"), []byte("digest"))
	s := configMsgServer{keeper: k}
	msg := &MsgUpdateConfig{Authority: authtypes.NewModuleAddress("not-governance").String(), GasLimit: 1000000}
	if _, err := s.UpdateConfig(sdk.WrapSDKContext(ctx), msg); err == nil {
		t.Fatal("unauthorized update accepted")
	}
	if len(k.Config(ctx).AllowedClientIDs) != len(initial.AllowedClientIDs) {
		t.Fatal("unauthorized update changed state")
	}
	msg.Authority = ConfigAuthority()
	msg.GasLimit = 1
	if _, err := s.UpdateConfig(sdk.WrapSDKContext(ctx), msg); err == nil {
		t.Fatal("invalid gas accepted")
	}
	msg.GasLimit = 1000000
	if _, err := s.UpdateConfig(sdk.WrapSDKContext(ctx), msg); err != nil {
		t.Fatal(err)
	}
	if len(k.Config(ctx).AllowedClientIDs) != 0 {
		t.Fatal("governance disable was not applied")
	}
	if string(ctx.KVStore(key).Get([]byte("event/existing"))) != "digest" {
		t.Fatal("replay state lost")
	}
}

func TestUpdateConfigCodecAndCLI(t *testing.T) {
	registry, err := codectypes.NewInterfaceRegistryWithOptions(codectypes.InterfaceRegistryOptions{
		ProtoFiles: proto.HybridResolver,
		SigningOptions: signing.Options{
			AddressCodec:          address.NewBech32Codec(sdk.GetConfig().GetBech32AccountAddrPrefix()),
			ValidatorAddressCodec: address.NewBech32Codec(sdk.GetConfig().GetBech32ValidatorAddrPrefix()),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	AppModuleBasic{}.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)
	msg := &MsgUpdateConfig{Authority: ConfigAuthority(), AllowedClientIds: []string{"fabric-msp-0"}, GasLimit: 1000000}
	signers, _, err := cdc.GetMsgV1Signers(msg)
	if err != nil {
		t.Fatal(err)
	}
	authority, _ := sdk.AccAddressFromBech32(ConfigAuthority())
	if len(signers) != 1 || !bytes.Equal(signers[0], authority) {
		t.Fatal("message signer must be governance")
	}
	packed, err := codectypes.NewAnyWithValue(msg)
	if err != nil {
		t.Fatal(err)
	}
	bz, err := cdc.MarshalJSON(packed)
	if err != nil {
		t.Fatal(err)
	}
	var decoded codectypes.Any
	if err := cdc.UnmarshalJSON(bz, &decoded); err != nil {
		t.Fatal(err)
	}
	var unpacked sdk.Msg
	if err := cdc.UnpackAny(&decoded, &unpacked); err != nil {
		t.Fatal(err)
	}
	if unpacked.(*MsgUpdateConfig).Authority != ConfigAuthority() {
		t.Fatal("authority changed during roundtrip")
	}
	root := newTxCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"update-config", "--allowed-client", "fabric-msp-0", "--generate-message"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var generated map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &generated); err != nil {
		t.Fatal(err)
	}
	if generated["@type"] != "/fabricevents.v1.MsgUpdateConfig" || generated["authority"] != ConfigAuthority() {
		t.Fatalf("invalid message: %s", out.String())
	}
}

func TestSignedOnlyConfigCLI(t *testing.T) {
	root := newTxCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"update-config", "--allowed-client", "fabric-msp-3", "--generate-message"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var generated map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &generated); err != nil {
		t.Fatal(err)
	}
	if _, exists := generated["contract_address"]; exists {
		t.Fatal("retired configuration field must not be generated")
	}
}
