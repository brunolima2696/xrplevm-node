package fabricevents

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"cosmossdk.io/core/appmodule"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/cosmos/cosmos-sdk/types/msgservice"
	authkeeper "github.com/cosmos/cosmos-sdk/x/auth/keeper"
	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/spf13/cobra"
)

type Entry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
type Genesis struct {
	PortID  string  `json:"port_id"`
	Config  Config  `json:"config"`
	Entries []Entry `json:"entries"`
}

func (g Genesis) Validate() error {
	if g.PortID != PortID {
		return fmt.Errorf("expected default port %q", PortID)
	}
	if err := g.Config.Validate(); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, entry := range g.Entries {
		if (!strings.HasPrefix(entry.Key, "event/") && !strings.HasPrefix(entry.Key, "payment/") && !strings.HasPrefix(entry.Key, "order/") && !strings.HasPrefix(entry.Key, "order_event/") && !strings.HasPrefix(entry.Key, "signed_tx/") && !strings.HasPrefix(entry.Key, "signed_order/")) || seen[entry.Key] {
			return fmt.Errorf("invalid or repeated genesis entry")
		}
		if value, err := hex.DecodeString(entry.Value); err != nil || len(value) == 0 {
			return fmt.Errorf("invalid genesis entry value")
		}
		seen[entry.Key] = true
	}
	return nil
}

type AppModuleBasic struct{}

func (AppModuleBasic) Name() string { return ModuleName }
func (AppModuleBasic) RegisterLegacyAminoCodec(cdc *codec.LegacyAmino) {
	cdc.RegisterConcrete(&MsgUpdateConfig{}, "fabricevents/UpdateConfig", nil)
}
func (AppModuleBasic) RegisterInterfaces(registry codectypes.InterfaceRegistry) {
	registry.RegisterImplementations((*sdk.Msg)(nil), &MsgUpdateConfig{})
	msgservice.RegisterMsgServiceDesc(registry, &_Msg_serviceDesc)
}
func (AppModuleBasic) RegisterGRPCGatewayRoutes(client.Context, *runtime.ServeMux) {}
func (AppModuleBasic) DefaultGenesis(codec.JSONCodec) json.RawMessage {
	bz, err := json.Marshal(Genesis{PortID: PortID, Config: DefaultConfig(), Entries: []Entry{}})
	if err != nil {
		panic(err)
	}
	return bz
}
func (AppModuleBasic) ValidateGenesis(_ codec.JSONCodec, _ client.TxEncodingConfig, bz json.RawMessage) error {
	var g Genesis
	if err := json.Unmarshal(bz, &g); err != nil {
		return err
	}
	return g.Validate()
}
func (AppModuleBasic) GetTxCmd() *cobra.Command { return newTxCmd() }
func (AppModuleBasic) GetQueryCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "fabric-events", Short: "Fabric IBC applications and signed EVM payments"}
	cmd.AddCommand(newConfigQueryCmd())
	cmd.AddCommand(newSignedTransactionQueryCmd(), newSignedOrderQueryCmd())
	cmd.AddCommand(&cobra.Command{Use: "authority", Short: "Print the governance authority address", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), ConfigAuthority())
		return err
	}})
	return cmd
}

type AppModule struct {
	AppModuleBasic
	Keeper   Keeper
	Accounts authkeeper.AccountKeeper
}

var _ module.HasGenesis = AppModule{}
var _ appmodule.AppModule = AppModule{}

func NewAppModule(k Keeper, accounts authkeeper.AccountKeeper) AppModule {
	return AppModule{Keeper: k, Accounts: accounts}
}
func (AppModule) IsOnePerModuleType()      {}
func (AppModule) IsAppModule()             {}
func (AppModule) ConsensusVersion() uint64 { return 1 }
func (am AppModule) RegisterServices(cfg module.Configurator) {
	RegisterMsgServer(cfg.MsgServer(), configMsgServer{keeper: am.Keeper})
}
func (am AppModule) InitGenesis(ctx sdk.Context, _ codec.JSONCodec, bz json.RawMessage) {
	var g Genesis
	if err := json.Unmarshal(bz, &g); err != nil {
		panic(err)
	}
	if err := g.Validate(); err != nil {
		panic(err)
	}
	am.Keeper.SetConfig(ctx, g.Config)
	am.Keeper.SetPort(ctx, g.PortID)
	am.Accounts.GetModuleAccount(ctx, ModuleName)
	for _, entry := range g.Entries {
		value, _ := hex.DecodeString(entry.Value)
		ctx.KVStore(am.Keeper.key).Set([]byte(entry.Key), value)
	}
}
func (am AppModule) ExportGenesis(ctx sdk.Context, _ codec.JSONCodec) json.RawMessage {
	g := Genesis{PortID: am.Keeper.Port(ctx), Config: am.Keeper.Config(ctx), Entries: []Entry{}}
	it := ctx.KVStore(am.Keeper.key).Iterator(nil, nil)
	defer it.Close()
	for ; it.Valid(); it.Next() {
		if string(it.Key()) != "config" && string(it.Key()) != "port_id" {
			g.Entries = append(g.Entries, Entry{Key: string(it.Key()), Value: hex.EncodeToString(it.Value())})
		}
	}
	bz, err := json.Marshal(g)
	if err != nil {
		panic(err)
	}
	return bz
}
