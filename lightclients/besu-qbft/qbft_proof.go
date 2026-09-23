package qbft

import (
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb/memorydb"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/trie"
)

func decodeMPTProofNodes(proofRLP []byte) ([][]byte, error) {
	var decoded [][][]byte
	if err := rlp.DecodeBytes(proofRLP, &decoded); err != nil {
		return nil, fmt.Errorf("failed to decode mpt proof: %w", err)
	}
	nodes := make([][]byte, 0, len(decoded))
	for _, item := range decoded {
		b, err := rlp.EncodeToBytes(item)
		if err != nil {
			return nil, fmt.Errorf("failed to re-encode mpt proof node: %w", err)
		}
		nodes = append(nodes, b)
	}
	return nodes, nil
}

func buildProofDB(nodes [][]byte) *memorydb.Database {
	db := memorydb.New()
	for _, n := range nodes {
		_ = db.Put(crypto.Keccak256(n), n)
	}
	return db
}

func storageTrieKey(path []byte) ([]byte, error) {
	storageKey := crypto.Keccak256Hash(append(
		crypto.Keccak256Hash(path).Bytes(),
		IBCCommitmentsSlot.Bytes()...,
	))
	return crypto.Keccak256(storageKey.Bytes()), nil
}

func verifyMPTMembership(storageRoot []byte, path []byte, proofRLP []byte) (found bool, value []byte, err error) {
	nodes, err := decodeMPTProofNodes(proofRLP)
	if err != nil {
		return false, nil, err
	}
	key, err := storageTrieKey(path)
	if err != nil {
		return false, nil, err
	}
	db := buildProofDB(nodes)
	raw, err := trie.VerifyProof(common.BytesToHash(storageRoot), key, db)
	if err != nil {
		return false, nil, fmt.Errorf("invalid mpt proof: %w", err)
	}
	if raw == nil {
		return false, nil, nil
	}
	var decoded []byte
	if err := rlp.DecodeBytes(raw, &decoded); err != nil {
		return false, nil, fmt.Errorf("failed to decode storage value: %w", err)
	}
	return true, decoded, nil
}

func pad32(b []byte) []byte {
	if len(b) >= 32 {
		return b[len(b)-32:]
	}
	out := make([]byte, 32)
	copy(out[32-len(b):], b)
	return out
}

type gethAccount struct {
	Nonce    uint64
	Balance  *big.Int
	Root     common.Hash
	CodeHash []byte
}

func verifyAccountStorageRoot(header *types.Header, accountProofNodes [][]byte, ibcStoreAddress []byte) ([]byte, error) {
	db := buildProofDB(accountProofNodes)
	key := crypto.Keccak256(ibcStoreAddress)
	raw, err := trie.VerifyProof(header.Root, key, db)
	if err != nil {
		return nil, fmt.Errorf("invalid account proof: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("ibc store account not found in state trie (address=%x)", ibcStoreAddress)
	}
	var acct gethAccount
	if err := rlp.DecodeBytes(raw, &acct); err != nil {
		return nil, fmt.Errorf("failed to decode account: %w", err)
	}
	return acct.Root.Bytes(), nil
}
