package fabricevents

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	channeltypes "github.com/cosmos/ibc-go/v10/modules/core/04-channel/types"
	porttypes "github.com/cosmos/ibc-go/v10/modules/core/05-port/types"
	"github.com/cosmos/ibc-go/v10/modules/core/exported"
)

type IBCModule struct{ Keeper Keeper }

var _ porttypes.IBCModule = IBCModule{}

func validateChannel(order channeltypes.Order, port string, cp channeltypes.Counterparty, version string) error {
	if order != channeltypes.UNORDERED || port != PortID || cp.PortId != PortID || (version != "" && version != Version) {
		return fmt.Errorf("expected unordered fabric-events / fabric-events-1 channel")
	}
	return nil
}
func (IBCModule) OnChanOpenInit(_ sdk.Context, order channeltypes.Order, _ []string, port, _ string, cp channeltypes.Counterparty, version string) (string, error) {
	return Version, validateChannel(order, port, cp, version)
}
func (m IBCModule) OnChanOpenTry(ctx sdk.Context, order channeltypes.Order, hops []string, port, channel string, cp channeltypes.Counterparty, version string) (string, error) {
	if version != Version {
		return "", fmt.Errorf("unsupported counterparty version")
	}
	return m.OnChanOpenInit(ctx, order, hops, port, channel, cp, version)
}
func (IBCModule) OnChanOpenAck(_ sdk.Context, _, _, _, version string) error {
	if version != Version {
		return fmt.Errorf("unsupported counterparty version")
	}
	return nil
}
func (IBCModule) OnChanOpenConfirm(sdk.Context, string, string) error  { return nil }
func (IBCModule) OnChanCloseInit(sdk.Context, string, string) error    { return nil }
func (IBCModule) OnChanCloseConfirm(sdk.Context, string, string) error { return nil }
func (m IBCModule) OnRecvPacket(ctx sdk.Context, version string, packet channeltypes.Packet, _ sdk.AccAddress) exported.Acknowledgement {
	if version != Version {
		return channeltypes.NewErrorAcknowledgement(fmt.Errorf("unsupported channel version"))
	}
	cached, write := ctx.CacheContext()
	data, err := DecodePacket(packet.Data)
	if err != nil {
		return channeltypes.NewErrorAcknowledgementWithCodespace(err)
	}
	result, err := m.Keeper.receiveSignedPacket(cached, packet, data)
	if err != nil {
		ctx.EventManager().EmitEvent(sdk.NewEvent("fabric_event_failed", sdk.NewAttribute("reason", err.Error())))
		return channeltypes.NewErrorAcknowledgementWithCodespace(err)
	}
	write()
	return channeltypes.NewResultAcknowledgement(result)
}
func (IBCModule) OnAcknowledgementPacket(sdk.Context, string, channeltypes.Packet, []byte, sdk.AccAddress) error {
	return fmt.Errorf("XRPL fabric-events is receive-only")
}
func (IBCModule) OnTimeoutPacket(sdk.Context, string, channeltypes.Packet, sdk.AccAddress) error {
	return fmt.Errorf("XRPL fabric-events is receive-only")
}
