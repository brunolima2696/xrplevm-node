package fabricevents

import (
	"encoding/hex"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

func validOrderID(id string) bool {
	bz, err := hex.DecodeString(strings.TrimPrefix(id, "0x"))
	return err == nil && len(id) == 66 && strings.HasPrefix(id, "0x") && id == strings.ToLower(id) && len(bz) == 32 && common.BytesToHash(bz) != (common.Hash{})
}
