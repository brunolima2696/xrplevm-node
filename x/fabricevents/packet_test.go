package fabricevents

import (
	"testing"

	channeltypes "github.com/cosmos/ibc-go/v10/modules/core/04-channel/types"
	portkeeper "github.com/cosmos/ibc-go/v10/modules/core/05-port/keeper"
	porttypes "github.com/cosmos/ibc-go/v10/modules/core/05-port/types"
)

func TestFabricEventsPortRouting(t *testing.T) {
	r := porttypes.NewRouter().AddRoute(RouteKey, IBCModule{})
	k := portkeeper.NewKeeper()
	k.Router = r
	if _, ok := k.Route(PortID); !ok {
		t.Fatal("fabric-events is not routed")
	}
	if err := validateChannel(channeltypes.UNORDERED, PortID, channeltypes.Counterparty{PortId: PortID}, Version); err != nil {
		t.Fatal(err)
	}
	if err := validateChannel(channeltypes.UNORDERED, PortID, channeltypes.Counterparty{PortId: "transfer"}, "ics20-1"); err == nil {
		t.Fatal("transfer channel accepted")
	}
}
