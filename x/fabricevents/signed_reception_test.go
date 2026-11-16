package fabricevents

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	channeltypes "github.com/cosmos/ibc-go/v10/modules/core/04-channel/types"
	"github.com/ethereum/go-ethereum/common"
)

func signedPacketFixture(t *testing.T, f *signedFixture, raw []byte) PacketData {
	t.Helper()
	vp := TransactionVP{Context: []string{"https://www.w3.org/ns/credentials/v2"}, Type: []string{"VerifiablePresentation"}}
	vp.Payment.OrderID = f.id
	vp.Payment.RawTransaction = "0x" + hex.EncodeToString(raw)
	bz, err := json.Marshal(vp)
	if err != nil {
		t.Fatal(err)
	}
	return PacketData{Version: 3, SourceChainID: "fabric_chain_0", OrderID: f.id, Action: SignedTransactionAction, VP: bz}
}
func readSignedAck(t *testing.T, bz []byte, err error) SignedTransactionAck {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	var ack SignedTransactionAck
	if err := json.Unmarshal(bz, &ack); err != nil {
		t.Fatal(err)
	}
	return ack
}
func TestSignedPacketSchemaAndParsing(t *testing.T) {
	f := newSignedFixture(t)
	p := signedPacketFixture(t, f, f.raw(t, 2, 0, 1450001, nil, 60000, 3))
	bz, _ := json.Marshal(p)
	if _, err := DecodePacket(bz); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*PacketData){
		func(p *PacketData) { p.SourceChainID = "../bad" },
		func(p *PacketData) { p.Action = "unsupported" },
		func(p *PacketData) { p.OrderID = "0x" + strings.Repeat("22", 32) },
	} {
		q := p
		change(&q)
		if q.Validate() == nil {
			t.Fatal("invalid signed envelope accepted")
		}
	}
	for _, raw := range []string{
		strings.Replace(string(p.VP), `"raw_transaction":`, `"raw_transaction":"0xff","RAW_TRANSACTION":`, 1),
		strings.TrimSuffix(string(p.VP), "}") + `,"unexpected":true}`,
		string(p.VP) + " {}",
		`{"@context":[],"type":[],"payment":{}}`,
	} {
		if _, err := ParseTransactionVP([]byte(raw)); err == nil {
			t.Fatal("ambiguous/invalid VP accepted")
		}
	}
	for _, field := range []string{"payload", "request", "payment_id", "event_id"} {
		bad := strings.TrimSuffix(string(bz), "}") + `,"` + field + `":{}}`
		if _, err := DecodePacket([]byte(bad)); err == nil {
			t.Fatal("retired field accepted:", field)
		}
	}
	dup := strings.Replace(string(bz), `"version":3`, `"version":2,"VERSION":3`, 1)
	if _, err := DecodePacket([]byte(dup)); err == nil {
		t.Fatal("duplicate packet field accepted")
	}
}

func TestSignedIBCReplayPersistenceAndOutcome(t *testing.T) {
	for _, mode := range []string{"", "revert"} {
		t.Run("mode="+mode, func(t *testing.T) {
			f := newSignedFixture(t)
			f.engine.mode = mode
			p := signedPacketFixture(t, f, f.raw(t, 2, 0, 1450001, common.HexToHash(f.id).Bytes(), 60000, 3))
			k := NewKeeper(f.key, nil).WithSignedTransactionProcessor(f.processor)
			cfg := Config{AllowedClientIDs: []string{"fabric-msp-0"}, GasLimit: 1000000}
			bz, err := k.receiveAuthenticatedTransaction(f.ctx, p, cfg, "fabric-msp-0/channel-0")
			ack := readSignedAck(t, bz, err)
			want := "success"
			if mode == "revert" {
				want = "reverted"
			}
			if ack.Status != want || ack.Hash == "" || f.nonce() != 1 {
				t.Fatalf("%+v", ack)
			}
			var record SignedTransactionRecord
			if err := json.Unmarshal(f.ctx.KVStore(f.key).Get(signedHashKey(ack.Hash)), &record); err != nil {
				t.Fatal(err)
			}
			if record.Result.Input != f.id || record.Result.RawTransaction == "" || record.Result.Status != want {
				t.Fatal("missing transaction/input/result")
			}
			bz, err = k.receiveAuthenticatedTransaction(f.ctx, p, cfg, "fabric-msp-0/channel-0")
			if cached := readSignedAck(t, bz, err); cached.Hash != ack.Hash || cached.Status != want || f.engine.calls != 1 {
				t.Fatal("same packet executed twice")
			}
			bz, err = k.receiveAuthenticatedTransaction(f.ctx, p, cfg, "fabric-msp-1/channel-1")
			if readSignedAck(t, bz, err).Status != "rejected" || f.engine.calls != 1 {
				t.Fatal("cross-origin replay accepted")
			}
			other := signedPacketFixture(t, f, f.raw(t, 2, 1, 1450001, nil, 60000, 3))
			bz, err = k.receiveAuthenticatedTransaction(f.ctx, other, cfg, "fabric-msp-0/channel-0")
			if readSignedAck(t, bz, err).Status != "rejected" || f.nonce() != 1 {
				t.Fatal("same order reused for another signature")
			}
		})
	}
}

func TestSignedIBCRejectionDoesNotReserveOrder(t *testing.T) {
	f := newSignedFixture(t)
	k := NewKeeper(f.key, nil).WithSignedTransactionProcessor(f.processor)
	cfg := Config{AllowedClientIDs: []string{"fabric-msp-0"}, GasLimit: 1000000}
	for _, raw := range [][]byte{{0xff}, f.raw(t, 2, 1, 1450001, nil, 60000, 3)} {
		p := signedPacketFixture(t, f, raw)
		bz, err := k.receiveAuthenticatedTransaction(f.ctx, p, cfg, "fabric-msp-0/channel-0")
		if ack := readSignedAck(t, bz, err); ack.Status != "rejected" || ack.Reason == "" {
			t.Fatalf("%+v", ack)
		}
		if f.ctx.KVStore(f.key).Has(signedOrderKey(p.SourceChainID, p.OrderID)) || f.nonce() != 0 {
			t.Fatal("rejection consumed order/nonce")
		}
	}
	p := signedPacketFixture(t, f, f.raw(t, 2, 0, 1450001, nil, 60000, 3))
	bz, err := k.receiveAuthenticatedTransaction(f.ctx, p, cfg, "fabric-msp-0/channel-0")
	if readSignedAck(t, bz, err).Status != "success" {
		t.Fatal("valid retry blocked by earlier rejection")
	}
}

func TestSignedIBCFailureAndOuterRollback(t *testing.T) {
	f := newSignedFixture(t)
	f.engine.mode = "error"
	k := NewKeeper(f.key, nil).WithSignedTransactionProcessor(f.processor)
	cfg := Config{AllowedClientIDs: []string{"fabric-msp-0"}, GasLimit: 1000000}
	p := signedPacketFixture(t, f, f.raw(t, 2, 0, 1450001, nil, 60000, 3))
	cache, _ := f.ctx.CacheContext()
	if _, err := k.receiveAuthenticatedTransaction(cache, p, cfg, "fabric-msp-0/channel-0"); err == nil {
		t.Fatal("engine failure acknowledged")
	}
	f.assertUnchanged(t)
	f.engine.mode = ""
	cache, _ = f.ctx.CacheContext()
	if _, err := k.receiveAuthenticatedTransaction(cache, p, cfg, "fabric-msp-0/channel-0"); err != nil {
		t.Fatal(err)
	}
	f.assertUnchanged(t)
	if f.ctx.KVStore(f.key).Has(signedOrderKey(p.SourceChainID, p.OrderID)) {
		t.Fatal("outer failure leaked record")
	}
	bz, _ := json.Marshal(p)
	ack := (IBCModule{Keeper: NewKeeper(f.key, nil)}).OnRecvPacket(f.ctx, Version, channeltypes.Packet{Data: bz, SourcePort: PortID, DestinationPort: PortID}, sdk.AccAddress{})
	if ack.Success() {
		t.Fatal("unwired processor accepted an IBC payment")
	}
}

func TestSignedOnlyConfigurationAndHash(t *testing.T) {
	if err := (Config{AllowedClientIDs: []string{"fabric-msp-0"}, GasLimit: 1000000}).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, hash := range []string{"", "0x01", "../bad", "0x" + strings.Repeat("zz", 32)} {
		if _, err := normalizeSignedHash(hash); err == nil {
			t.Fatal("invalid lookup hash")
		}
	}
}
