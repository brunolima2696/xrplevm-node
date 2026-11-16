package fabricevents

import (
	"encoding/json"
	"testing"

	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/testutil"
)

func TestDefaultGenesisAndExportPort(t *testing.T) {
	var genesis Genesis
	if err := json.Unmarshal((AppModuleBasic{}).DefaultGenesis(nil), &genesis); err != nil {
		t.Fatal(err)
	}
	if genesis.PortID != "fabric-events" {
		t.Fatalf("default port: %q", genesis.PortID)
	}
	if err := genesis.Validate(); err != nil {
		t.Fatal(err)
	}
	key := storetypes.NewKVStoreKey(ModuleName)
	ctx := testutil.DefaultContext(key, storetypes.NewTransientStoreKey("test"))
	k := NewKeeper(key, nil)
	k.SetPort(ctx, genesis.PortID)
	k.SetConfig(ctx, genesis.Config)
	if err := json.Unmarshal((AppModule{Keeper: k}).ExportGenesis(ctx, nil), &genesis); err != nil {
		t.Fatal(err)
	}
	if genesis.PortID != PortID || len(genesis.Entries) != 0 {
		t.Fatalf("port must be exported separately from replay state: %+v", genesis)
	}
	genesis.PortID = "transfer"
	if err := genesis.Validate(); err == nil {
		t.Fatal("unexpected port accepted")
	}
}
