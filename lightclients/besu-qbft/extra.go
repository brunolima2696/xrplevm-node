package qbft

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
)

// Storage slot used by the YUI IBCHandler commitments mapping.
var IBCCommitmentsSlot = common.HexToHash("1ee222554989dda120e26ecacf756fe1235cd8d726706b57517715dde4f0c900")

func ecrecover(hash, sig []byte) (common.Address, error) {
	pub, err := crypto.SigToPub(hash, sig)
	if err != nil {
		return common.Address{}, err
	}
	return crypto.PubkeyToAddress(*pub), nil
}

type ExtraData struct {
	Vanity     []byte
	Validators []common.Address
	Vote       rlp.RawValue
	Round      []byte
	Seals      [][]byte
}

func parseExtraData(data []byte) (*ExtraData, error) {
	var extra ExtraData
	if err := rlp.DecodeBytes(data, &extra); err != nil {
		return nil, err
	}
	return &extra, nil
}
