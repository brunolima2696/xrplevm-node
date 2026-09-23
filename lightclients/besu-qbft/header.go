package qbft

import (
	"fmt"
	"log"

	clienttypes "github.com/cosmos/ibc-go/v10/modules/core/02-client/types"
	"github.com/cosmos/ibc-go/v10/modules/core/exported"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
)

var _ exported.ClientMessage = (*Header)(nil)

func (Header) ClientType() string {
	return QBFT_CLIENT_TYPE
}

func (h *Header) GetHeight() exported.Height {
	ethHeader, err := h.decodeEthHeader()
	if err != nil {
		log.Panicf("invalid header: %v", h)
	}
	return ethHeightToPB(ethHeader.Number.Uint64())
}

func (h *Header) ValidateBasic() error {
	if _, err := h.decodeEthHeader(); err != nil {
		return err
	}
	if _, err := h.decodeAccountProof(); err != nil {
		return err
	}
	return nil
}

func (h *Header) decodeEthHeader() (*types.Header, error) {
	var ethHeader types.Header
	if err := rlp.DecodeBytes(h.BesuHeaderRlp, &ethHeader); err != nil {
		return nil, err
	}
	if ethHeader.Number == nil || !ethHeader.Number.IsUint64() || ethHeader.Number.Sign() <= 0 {
		return nil, fmt.Errorf("invalid Besu block number")
	}
	return &ethHeader, nil
}

func (h *Header) decodeAccountProof() ([][]byte, error) {
	return decodeMPTProofNodes(h.AccountStateProof)
}

func ethHeightToPB(height uint64) clienttypes.Height {
	return clienttypes.NewHeight(0, uint64(height))
}
