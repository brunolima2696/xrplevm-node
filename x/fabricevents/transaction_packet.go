package fabricevents

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const SignedTransactionAction = "executeTransaction"

// The VP is a transport envelope. The inner Ethereum signature is the payment
// authorization; this is not a claim of general W3C credential/DID verification.
type TransactionVP struct {
	Context []string `json:"@context"`
	Type    []string `json:"type"`
	Payment struct {
		OrderID        string `json:"id_pedido"`
		RawTransaction string `json:"raw_transaction"`
	} `json:"payment"`
}

func ParseTransactionVP(raw []byte) (TransactionVP, error) {
	var vp TransactionVP
	if len(raw) == 0 || len(raw) > 10000 {
		return vp, fmt.Errorf("invalid transaction VP size")
	}
	if err := validateTransactionJSON(raw); err != nil {
		return vp, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&vp); err != nil {
		return vp, err
	}
	if len(vp.Context) != 1 || vp.Context[0] != "https://www.w3.org/ns/credentials/v2" || len(vp.Type) != 1 || vp.Type[0] != "VerifiablePresentation" || !validOrderID(vp.Payment.OrderID) {
		return vp, fmt.Errorf("invalid transaction VP context, type or id_pedido")
	}
	bz, err := hex.DecodeString(strings.TrimPrefix(vp.Payment.RawTransaction, "0x"))
	if !strings.HasPrefix(vp.Payment.RawTransaction, "0x") || err != nil || len(bz) == 0 || len(bz) > 4096 {
		return vp, fmt.Errorf("raw_transaction must be hex signed Ethereum bytes, at most 4096 bytes")
	}
	return vp, nil
}

func validateTransactionJSON(raw []byte) error {
	if !utf8.Valid(raw) {
		return fmt.Errorf("invalid UTF-8 JSON")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	var visit func(int) error
	visit = func(depth int) error {
		if depth > 12 {
			return fmt.Errorf("JSON nesting too deep")
		}
		t, err := d.Token()
		if err != nil {
			return err
		}
		delim, nested := t.(json.Delim)
		if !nested {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok {
					return fmt.Errorf("invalid JSON key")
				}
				name = strings.ToLower(name)
				if seen[name] {
					return fmt.Errorf("duplicate JSON key")
				}
				seen[name] = true
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("invalid JSON delimiter")
		}
		_, err = d.Token()
		return err
	}
	if err := visit(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	return nil
}

func validTransactionSource(source string) bool {
	if len(source) == 0 || len(source) > 128 {
		return false
	}
	for _, c := range source {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

func (p PacketData) validateTransactionPacket() error {
	if !validTransactionSource(p.SourceChainID) || p.Action != SignedTransactionAction || !validOrderID(p.OrderID) {
		return fmt.Errorf("invalid version 3 transaction packet")
	}
	vp, err := ParseTransactionVP(p.VP)
	if err != nil {
		return err
	}
	if vp.Payment.OrderID != p.OrderID {
		return fmt.Errorf("packet/VP id_pedido mismatch")
	}
	return nil
}
