package fabricevents

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

const (
	ModuleName = "fabricevents"
	PortID     = "fabric-events"
	// ibc-go v10 matches alphanumeric route prefixes against port identifiers.
	RouteKey       = "fabric"
	Version        = "fabric-events-1"
	MaxPacketBytes = 12288
)

type PacketData struct {
	Version       uint32          `json:"version"`
	SourceChainID string          `json:"source_chain_id"`
	Action        string          `json:"action"`
	OrderID       string          `json:"id_pedido"`
	VP            json.RawMessage `json:"vp"`
}

func DecodePacket(bz []byte) (PacketData, error) {
	var p PacketData
	if len(bz) == 0 || len(bz) > MaxPacketBytes {
		return p, fmt.Errorf("invalid packet size")
	}
	if err := validateTransactionJSON(bz); err != nil {
		return p, err
	}
	d := json.NewDecoder(bytes.NewReader(bz))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return p, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return p, fmt.Errorf("unexpected trailing JSON")
	}
	return p, p.Validate()
}
func (p PacketData) Validate() error {
	if p.Version != 3 {
		return fmt.Errorf("unsupported packet version: expected 3")
	}
	return p.validateTransactionPacket()
}
