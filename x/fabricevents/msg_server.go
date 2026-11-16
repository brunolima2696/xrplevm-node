package fabricevents

import (
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	fabricmsp "github.com/xrplevm/node/v10/lightclients/fabric-msp"
)

// ConfigAuthority is the standard governance module account, not the deployer.
func ConfigAuthority() string { return authtypes.NewModuleAddress(govtypes.ModuleName).String() }

func (m *MsgUpdateConfig) configuration() Config {
	return Config{AllowedClientIDs: m.AllowedClientIds, GasLimit: m.GasLimit}
}

func (m *MsgUpdateConfig) ValidateBasic() error {
	if _, err := sdk.AccAddressFromBech32(m.Authority); err != nil {
		return fmt.Errorf("invalid authority: %w", err)
	}
	return m.configuration().Validate()
}

type configMsgServer struct{ keeper Keeper }

var _ MsgServer = configMsgServer{}

func (s configMsgServer) UpdateConfig(goCtx context.Context, msg *MsgUpdateConfig) (*MsgUpdateConfigResponse, error) {
	if msg == nil {
		return nil, fmt.Errorf("missing update configuration message")
	}
	if msg.Authority != ConfigAuthority() {
		return nil, fmt.Errorf("%w: expected governance authority %s", govtypes.ErrInvalidSigner, ConfigAuthority())
	}
	if err := msg.ValidateBasic(); err != nil {
		return nil, err
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	config := msg.configuration()
	for _, id := range config.AllowedClientIDs {
		state, found := s.keeper.ibc.ClientKeeper.GetClientState(ctx, id)
		if !found {
			return nil, fmt.Errorf("Fabric client not found: %s", id)
		}
		if _, ok := state.(*fabricmsp.ClientState); !ok {
			return nil, fmt.Errorf("client %s is not a Fabric MSP client", id)
		}
	}
	// Replay entries are not modified.
	s.keeper.SetConfig(ctx, config)
	ctx.EventManager().EmitEvent(sdk.NewEvent("fabric_events_config_updated",
		sdk.NewAttribute("authority", msg.Authority)))
	return &MsgUpdateConfigResponse{}, nil
}
