package eip1898

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
)

// BlockNumberOrHash can be used to specify a block by either its number or hash.
// This implements EIP-1898 standard for specifying blocks in JSON-RPC calls.
type BlockNumberOrHash struct {
	BlockNumber      *big.Int     `json:"blockNumber,omitempty"`
	BlockHash        *common.Hash `json:"blockHash,omitempty"`
	RequireCanonical bool         `json:"requireCanonical,omitempty"`
}

// NewBlockNumberOrHash creates a new BlockNumberOrHash from a block number or hash
func NewBlockNumberOrHash(blockNrOrHash interface{}) (*BlockNumberOrHash, error) {
	switch v := blockNrOrHash.(type) {
	case *big.Int:
		return &BlockNumberOrHash{BlockNumber: v}, nil
	case big.Int:
		val := v
		return &BlockNumberOrHash{BlockNumber: &val}, nil
	case string:
		if v == "latest" || v == "pending" || v == "earliest" {
			return &BlockNumberOrHash{BlockNumber: nil}, nil
		}
		if len(v) > 2 && v[:2] == "0x" {
			hash := common.HexToHash(v)
			return &BlockNumberOrHash{BlockHash: &hash}, nil
		}
		return nil, fmt.Errorf("invalid block number or hash: %s", v)
	case common.Hash:
		hash := v
		return &BlockNumberOrHash{BlockHash: &hash}, nil
	case *common.Hash:
		return &BlockNumberOrHash{BlockHash: v}, nil
	case BlockNumberOrHash:
		return &v, nil
	case *BlockNumberOrHash:
		return v, nil
	}
	return nil, fmt.Errorf("invalid block number or hash: %v", blockNrOrHash)
}

// MarshalJSON implements the json.Marshaler interface
func (b BlockNumberOrHash) MarshalJSON() ([]byte, error) {
	if b.BlockHash != nil {
		// Block hash specified
		result := map[string]interface{}{
			"blockHash": b.BlockHash.Hex(),
		}
		if b.RequireCanonical {
			result["requireCanonical"] = b.RequireCanonical
		}
		return json.Marshal(result)
	}
	if b.BlockNumber != nil {
		// Block number specified
		return json.Marshal(hexutil.EncodeBig(b.BlockNumber))
	}
	// Neither specified, return "latest"
	return json.Marshal("latest")
}

// UnmarshalJSON implements the json.Unmarshaler interface
func (b *BlockNumberOrHash) UnmarshalJSON(data []byte) error {
	// Try to unmarshal as a simple hex string (block number)
	var blockNum *hexutil.Big
	if err := json.Unmarshal(data, &blockNum); err == nil {
		if blockNum != nil {
			b.BlockNumber = (*big.Int)(blockNum)
		}
		return nil
	}

	// Try to unmarshal as a string
	var blockStr string
	if err := json.Unmarshal(data, &blockStr); err == nil {
		if blockStr == "latest" || blockStr == "pending" || blockStr == "earliest" {
			return nil // Leave both fields nil for these special values
		}
		if len(blockStr) > 2 && blockStr[:2] == "0x" {
			hash := common.HexToHash(blockStr)
			b.BlockHash = &hash
			return nil
		}
		return fmt.Errorf("invalid block number or hash string: %s", blockStr)
	}

	// Try to unmarshal as an object
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err == nil {
		if hash, ok := obj["blockHash"].(string); ok {
			h := common.HexToHash(hash)
			b.BlockHash = &h
		}
		if num, ok := obj["blockNumber"].(string); ok {
			if len(num) > 2 && num[:2] == "0x" {
				n, err := hexutil.DecodeBig(num)
				if err != nil {
					return err
				}
				b.BlockNumber = n
			}
		}
		if reqCanon, ok := obj["requireCanonical"].(bool); ok {
			b.RequireCanonical = reqCanon
		}
		return nil
	}

	return errors.New("invalid block number or hash")
}

// CallWithEIP1898 performs a JSON-RPC call with EIP-1898 block reference parameter
func CallWithEIP1898(ctx context.Context, c *rpc.Client, result interface{}, method string, blockRef BlockNumberOrHash, args ...interface{}) error {
	callArgs := make([]interface{}, 0, len(args)+1)
	callArgs = append(callArgs, args...)
	callArgs = append(callArgs, blockRef)
	return c.CallContext(ctx, result, method, callArgs...)
}

// String returns a string representation of the block reference
func (b BlockNumberOrHash) String() string {
	if b.BlockHash != nil {
		return fmt.Sprintf("blockhash:%s", b.BlockHash.Hex())
	}
	if b.BlockNumber != nil {
		return fmt.Sprintf("blocknumber:%s", hexutil.EncodeBig(b.BlockNumber))
	}
	return "latest"
}
