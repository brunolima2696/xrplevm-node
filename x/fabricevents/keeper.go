package fabricevents

import (
	"encoding/json"
	"fmt"
	"strings"

	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	channeltypes "github.com/cosmos/ibc-go/v10/modules/core/04-channel/types"
	ibckeeper "github.com/cosmos/ibc-go/v10/modules/core/keeper"
	fabricmsp "github.com/xrplevm/node/v10/lightclients/fabric-msp"
)

// Config is consensus state initialized in genesis and updated by governance.
// An empty client allowlist disables execution.
type Config struct {
	AllowedClientIDs []string `json:"allowed_client_ids"`
	GasLimit         uint64   `json:"gas_limit"`
}

func DefaultConfig() Config { return Config{AllowedClientIDs: []string{}, GasLimit: 1000000} }
func (c Config) Validate() error {
	if c.GasLimit < 100000 || c.GasLimit > 3000000 {
		return fmt.Errorf("gas_limit must be between 100000 and 3000000")
	}
	seen := map[string]bool{}
	for _, id := range c.AllowedClientIDs {
		parts := strings.Split(id, "fabric-msp-")
		if len(parts) != 2 || parts[0] != "" || parts[1] == "" || seen[id] {
			return fmt.Errorf("invalid or repeated fabric-msp client ID")
		}
		for _, ch := range parts[1] {
			if ch < '0' || ch > '9' {
				return fmt.Errorf("invalid fabric-msp client ID")
			}
		}
		seen[id] = true
	}
	return nil
}

type Keeper struct {
	key             storetypes.StoreKey
	ibc             *ibckeeper.Keeper
	signedProcessor *SignedTransactionProcessor
}

func NewKeeper(key storetypes.StoreKey, ibc *ibckeeper.Keeper) Keeper {
	return Keeper{key: key, ibc: ibc}
}

func (k Keeper) WithSignedTransactionProcessor(p *SignedTransactionProcessor) Keeper {
	k.signedProcessor = p
	return k
}

func (k Keeper) SetConfig(ctx sdk.Context, c Config) {
	if err := c.Validate(); err != nil {
		panic(err)
	}
	bz, err := json.Marshal(c)
	if err != nil {
		panic(err)
	}
	ctx.KVStore(k.key).Set([]byte("config"), bz)
}
func (k Keeper) Config(ctx sdk.Context) Config {
	bz := ctx.KVStore(k.key).Get([]byte("config"))
	if len(bz) == 0 {
		return DefaultConfig()
	}
	var c Config
	if err := json.Unmarshal(bz, &c); err != nil {
		panic(err)
	}
	return c
}

func (k Keeper) SetPort(ctx sdk.Context, port string) {
	if port != PortID {
		panic("invalid fabric-events port")
	}
	ctx.KVStore(k.key).Set([]byte("port_id"), []byte(port))
}
func (k Keeper) Port(ctx sdk.Context) string {
	return string(ctx.KVStore(k.key).Get([]byte("port_id")))
}

func (k Keeper) sourceClient(ctx sdk.Context, packet channeltypes.Packet, p PacketData, config Config) (string, error) {
	ch, found := k.ibc.ChannelKeeper.GetChannel(ctx, PortID, packet.DestinationChannel)
	if !found || ch.Version != Version || len(ch.ConnectionHops) != 1 || ch.Counterparty.PortId != PortID || ch.Counterparty.ChannelId != packet.SourceChannel {
		return "", fmt.Errorf("invalid fabric-events channel")
	}
	conn, found := k.ibc.ConnectionKeeper.GetConnection(ctx, ch.ConnectionHops[0])
	if !found {
		return "", fmt.Errorf("connection not found")
	}
	allowed := false
	for _, id := range config.AllowedClientIDs {
		if id == conn.ClientId {
			allowed = true
		}
	}
	if !allowed {
		return "", fmt.Errorf("Fabric client is not authorized")
	}
	state, found := k.ibc.ClientKeeper.GetClientState(ctx, conn.ClientId)
	if !found {
		return "", fmt.Errorf("client not found")
	}
	fabric, ok := state.(*fabricmsp.ClientState)
	if !ok || fabric.Id != p.SourceChainID {
		return "", fmt.Errorf("packet origin does not match Fabric client")
	}
	return conn.ClientId, nil
}
