package fabricevents

import "testing"

func TestRetiredPacketVersionRejected(t *testing.T) {
	if _, err := DecodePacket([]byte(`{"version":2,"source_chain_id":"fabric_chain_0","action":"unsupported"}`)); err == nil {
		t.Fatal("retired packet version accepted")
	}
}
