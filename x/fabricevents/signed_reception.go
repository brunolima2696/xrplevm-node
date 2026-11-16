package fabricevents

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/cosmos/cosmos-sdk/types"
	channeltypes "github.com/cosmos/ibc-go/v10/modules/core/04-channel/types"
	"github.com/ethereum/go-ethereum/common"
)

type SignedTransactionAck struct {
	OrderID string `json:"id_pedido"`
	Status  string `json:"status"`
	Hash    string `json:"hash,omitempty"`
	Reason  string `json:"reason,omitempty"`
	GasUsed uint64 `json:"gas_used,omitempty"`
}

type SignedTransactionRecord struct {
	SourceChainID string                  `json:"source_chain_id"`
	Binding       string                  `json:"binding"`
	Digest        string                  `json:"digest"`
	OuterTxHash   string                  `json:"outer_tx_hash"`
	Result        SignedTransactionResult `json:"result"`
}

func signedHashKey(hash string) []byte        { return []byte("signed_tx/" + strings.ToLower(hash)) }
func signedOrderKey(source, id string) []byte { return []byte("signed_order/" + source + "/" + id) }

func (k Keeper) receiveSignedPacket(ctx sdk.Context, packet channeltypes.Packet, data PacketData) ([]byte, error) {
	if packet.SourcePort != PortID || packet.DestinationPort != PortID || data.Version != 3 {
		return nil, fmt.Errorf("invalid signed transaction route")
	}
	if k.signedProcessor == nil {
		return nil, ErrSignedTransactionsDisabled
	}
	cfg := k.Config(ctx)
	clientID, err := k.sourceClient(ctx, packet, data, cfg)
	if err != nil {
		return nil, err
	}
	// Authorization is checked BEFORE consulting replay state, including cached
	// responses. A revoked Fabric client cannot replay an old successful order.
	return k.receiveAuthenticatedTransaction(ctx, data, cfg, clientID+"/"+packet.DestinationChannel)
}

func (k Keeper) receiveAuthenticatedTransaction(ctx sdk.Context, data PacketData, cfg Config, binding string) ([]byte, error) {
	if data.Version != 3 {
		return nil, fmt.Errorf("expected signed transaction packet")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := data.Validate(); err != nil {
		return nil, err
	}
	if k.signedProcessor == nil || !k.signedProcessor.options.Enabled {
		return nil, ErrSignedTransactionsDisabled
	}
	vp, _ := ParseTransactionVP(data.VP)
	raw, _ := hex.DecodeString(vp.Payment.RawTransaction[2:])
	digest := sha256.Sum256(raw)
	digestHex := hex.EncodeToString(digest[:])
	store := ctx.KVStore(k.key)
	key := signedOrderKey(data.SourceChainID, data.OrderID)
	reject := func(reason, hash string) ([]byte, error) {
		return json.Marshal(SignedTransactionAck{OrderID: data.OrderID, Status: "rejected", Hash: hash, Reason: reason})
	}
	if bz := store.Get(key); bz != nil {
		var record SignedTransactionRecord
		if err := json.Unmarshal(bz, &record); err != nil {
			return nil, err
		}
		if record.Binding != binding || record.Digest != digestHex {
			return reject("id_pedido already executed with another transaction or origin", "")
		}
		return signedResultAck(record.Result)
	}
	// A signature/nonce accepted through another IBC order must not execute twice.
	if tx, err := decodeSignedTransaction(raw); err == nil {
		if bz := store.Get(signedHashKey(tx.Hash().Hex())); bz != nil {
			var record SignedTransactionRecord
			if err := json.Unmarshal(bz, &record); err != nil {
				return nil, err
			}
			if record.Result.Status != "rejected" {
				return reject("signed transaction was already executed", tx.Hash().Hex())
			}
		}
	}
	// A future change of governance gas cap applies without recompiling the node.
	processor := *k.signedProcessor
	if cfg.GasLimit < processor.options.MaxGas {
		processor.options.MaxGas = cfg.GasLimit
	}
	result, err := processor.Process(ctx, raw, data.OrderID)
	if err != nil {
		return nil, err
	}
	outer := sha256.Sum256(ctx.TxBytes())
	record := SignedTransactionRecord{SourceChainID: data.SourceChainID, Binding: binding, Digest: digestHex, OuterTxHash: strings.ToUpper(hex.EncodeToString(outer[:])), Result: result}
	bz, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	// Rejections are queryable by hash when decodable, but do not reserve an ID
	// or consume a nonce: the same signature can be retried after balance changes.
	if result.Hash != "" {
		store.Set(signedHashKey(result.Hash), bz)
	}
	if result.Status == "success" || result.Status == "reverted" {
		store.Set(key, bz)
	}
	ctx.EventManager().EmitEvent(sdk.NewEvent("fabric_signed_transaction",
		sdk.NewAttribute("id_pedido", data.OrderID), sdk.NewAttribute("evm_hash", result.Hash), sdk.NewAttribute("status", result.Status)))
	return signedResultAck(result)
}

func signedResultAck(result SignedTransactionResult) ([]byte, error) {
	return json.Marshal(SignedTransactionAck{OrderID: result.OrderID, Status: result.Status, Hash: result.Hash, Reason: result.Reason, GasUsed: result.GasUsed})
}

func normalizeSignedHash(hash string) (string, error) {
	bz, err := hex.DecodeString(strings.TrimPrefix(hash, "0x"))
	if err != nil || !strings.HasPrefix(hash, "0x") || len(bz) != 32 {
		return "", fmt.Errorf("expected Ethereum hash (0x + 64 hex digits)")
	}
	return common.BytesToHash(bz).Hex(), nil
}
