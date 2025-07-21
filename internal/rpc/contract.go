package rpc

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
)

// ContractInfo represents information about a contract
type ContractInfo struct {
	Address     string                 `json:"address"`
	Type        string                 `json:"type"`        // ERC20, ERC721, ERC1155, Unknown
	Name        string                 `json:"name"`
	Symbol      string                 `json:"symbol"`
	Decimals    int                    `json:"decimals"`
	TotalSupply string                 `json:"total_supply"`
	Metadata    map[string]interface{} `json:"metadata"`
}

// TokenBalance represents a token balance for an address
type TokenBalance struct {
	Contract string `json:"contract"`
	Balance  string `json:"balance"`
	Symbol   string `json:"symbol"`
	Decimals int    `json:"decimals"`
}

// Standard ERC signatures
var (
	// ERC20 function signatures
	ERC20_NAME         = "0x06fdde03" // name()
	ERC20_SYMBOL       = "0x95d89b41" // symbol()
	ERC20_DECIMALS     = "0x313ce567" // decimals()
	ERC20_TOTAL_SUPPLY = "0x18160ddd" // totalSupply()
	ERC20_BALANCE_OF   = "0x70a08231" // balanceOf(address)
	ERC20_TRANSFER     = "0xa9059cbb" // transfer(address,uint256)
	
	// ERC721 function signatures
	ERC721_NAME             = "0x06fdde03" // name()
	ERC721_SYMBOL           = "0x95d89b41" // symbol()
	ERC721_TOKEN_URI        = "0xc87b56dd" // tokenURI(uint256)
	ERC721_OWNER_OF         = "0x6352211e" // ownerOf(uint256)
	ERC721_BALANCE_OF       = "0x70a08231" // balanceOf(address)
	ERC721_SUPPORTS_INTERFACE = "0x01ffc9a7" // supportsInterface(bytes4)
	
	// ERC1155 function signatures
	ERC1155_URI                = "0x0e89341c" // uri(uint256)
	ERC1155_BALANCE_OF         = "0x00fdd58e" // balanceOf(address,uint256)
	ERC1155_SUPPORTS_INTERFACE = "0x01ffc9a7" // supportsInterface(bytes4)
	
	// Interface IDs
	ERC165_INTERFACE_ID  = "0x01ffc9a7"
	ERC20_INTERFACE_ID   = "0x36372b07"
	ERC721_INTERFACE_ID  = "0x80ac58cd"
	ERC1155_INTERFACE_ID = "0xd9b67a26"
)

// GetContractInfo fetches comprehensive contract information using RPC calls
func (c *Client) GetContractInfo(ctx context.Context, contractAddress string) (*ContractInfo, error) {
	info := &ContractInfo{
		Address:  contractAddress,
		Type:     "Unknown",
		Metadata: make(map[string]interface{}),
	}

	// Check if it's a contract (has code)
	code, err := c.getCode(ctx, contractAddress)
	if err != nil || code == "0x" || code == "" {
		return info, nil // Not a contract
	}

	// Try to detect contract type by checking supported interfaces
	contractType, err := c.detectContractType(ctx, contractAddress)
	if err == nil {
		info.Type = contractType
	}

	// Fetch contract metadata based on type
	switch info.Type {
	case "ERC20":
		c.fetchERC20Info(ctx, contractAddress, info)
	case "ERC721":
		c.fetchERC721Info(ctx, contractAddress, info)
	case "ERC1155":
		c.fetchERC1155Info(ctx, contractAddress, info)
	default:
		// Try ERC20 methods even if interface detection failed
		c.tryFetchERC20Info(ctx, contractAddress, info)
	}

	return info, nil
}

// detectContractType determines the contract type by checking supported interfaces
func (c *Client) detectContractType(ctx context.Context, contractAddress string) (string, error) {
	// Try ERC165 supportsInterface first
	if c.supportsInterface(ctx, contractAddress, ERC1155_INTERFACE_ID) {
		return "ERC1155", nil
	}
	if c.supportsInterface(ctx, contractAddress, ERC721_INTERFACE_ID) {
		return "ERC721", nil
	}
	
	// ERC20 doesn't always implement ERC165, so check by trying methods
	if c.hasERC20Methods(ctx, contractAddress) {
		return "ERC20", nil
	}
	
	return "Unknown", nil
}

// supportsInterface checks if contract supports a given interface
func (c *Client) supportsInterface(ctx context.Context, contractAddress, interfaceID string) bool {
	// Call supportsInterface(bytes4)
	callData := ERC721_SUPPORTS_INTERFACE + padLeft(interfaceID[2:], 64)
	result, err := c.ethCall(ctx, contractAddress, callData)
	if err != nil {
		return false
	}
	
	// Parse boolean result
	if len(result) >= 64 {
		return result[len(result)-1:] == "1"
	}
	return false
}

// hasERC20Methods checks if contract has basic ERC20 methods
func (c *Client) hasERC20Methods(ctx context.Context, contractAddress string) bool {
	// Try calling decimals() - most ERC20 tokens have this
	_, err := c.ethCall(ctx, contractAddress, ERC20_DECIMALS)
	if err != nil {
		return false
	}
	
	// Try calling symbol()
	_, err = c.ethCall(ctx, contractAddress, ERC20_SYMBOL)
	return err == nil
}

// fetchERC20Info fetches ERC20 token information
func (c *Client) fetchERC20Info(ctx context.Context, contractAddress string, info *ContractInfo) {
	// Fetch name
	if nameResult, err := c.ethCall(ctx, contractAddress, ERC20_NAME); err == nil {
		if name := c.decodeString(nameResult); name != "" {
			info.Name = name
		}
	}
	
	// Fetch symbol
	if symbolResult, err := c.ethCall(ctx, contractAddress, ERC20_SYMBOL); err == nil {
		if symbol := c.decodeString(symbolResult); symbol != "" {
			info.Symbol = symbol
		}
	}
	
	// Fetch decimals
	if decimalsResult, err := c.ethCall(ctx, contractAddress, ERC20_DECIMALS); err == nil {
		if decimals := c.decodeUint256(decimalsResult); decimals != nil {
			info.Decimals = int(decimals.Int64())
		}
	}
	
	// Fetch total supply
	if supplyResult, err := c.ethCall(ctx, contractAddress, ERC20_TOTAL_SUPPLY); err == nil {
		if supply := c.decodeUint256(supplyResult); supply != nil {
			info.TotalSupply = supply.String()
		}
	}
}

// tryFetchERC20Info tries to fetch ERC20 info even if interface detection failed
func (c *Client) tryFetchERC20Info(ctx context.Context, contractAddress string, info *ContractInfo) {
	// Try fetching ERC20 info and update type if successful
	originalType := info.Type
	c.fetchERC20Info(ctx, contractAddress, info)
	
	// If we successfully got token info, update the type
	if info.Symbol != "" || info.Name != "" {
		info.Type = "ERC20"
	} else {
		info.Type = originalType
	}
}

// fetchERC721Info fetches ERC721 NFT information
func (c *Client) fetchERC721Info(ctx context.Context, contractAddress string, info *ContractInfo) {
	// Fetch name
	if nameResult, err := c.ethCall(ctx, contractAddress, ERC721_NAME); err == nil {
		if name := c.decodeString(nameResult); name != "" {
			info.Name = name
		}
	}
	
	// Fetch symbol
	if symbolResult, err := c.ethCall(ctx, contractAddress, ERC721_SYMBOL); err == nil {
		if symbol := c.decodeString(symbolResult); symbol != "" {
			info.Symbol = symbol
		}
	}
	
	// For NFTs, we can't get total supply easily without additional methods
	info.Metadata["type"] = "NFT"
}

// fetchERC1155Info fetches ERC1155 token information
func (c *Client) fetchERC1155Info(ctx context.Context, contractAddress string, info *ContractInfo) {
	info.Metadata["type"] = "Multi-Token"
	
	// ERC1155 doesn't have standard name/symbol methods
	// Would need to check for optional extensions or use external APIs
}

// GetTokenBalance fetches token balance for an address
func (c *Client) GetTokenBalance(ctx context.Context, contractAddress, walletAddress string) (*TokenBalance, error) {
	// Get contract info first
	contractInfo, err := c.GetContractInfo(ctx, contractAddress)
	if err != nil {
		return nil, err
	}
	
	if contractInfo.Type != "ERC20" {
		return nil, fmt.Errorf("contract is not an ERC20 token")
	}
	
	// Call balanceOf(address)
	callData := ERC20_BALANCE_OF + padLeft(walletAddress[2:], 64)
	result, err := c.ethCall(ctx, contractAddress, callData)
	if err != nil {
		return nil, err
	}
	
	balance := c.decodeUint256(result)
	if balance == nil {
		return nil, fmt.Errorf("failed to decode balance")
	}
	
	return &TokenBalance{
		Contract: contractAddress,
		Balance:  balance.String(),
		Symbol:   contractInfo.Symbol,
		Decimals: contractInfo.Decimals,
	}, nil
}

// GetNFTOwner fetches the owner of an NFT
func (c *Client) GetNFTOwner(ctx context.Context, contractAddress, tokenID string) (string, error) {
	// Convert tokenID to uint256
	tokenIDBig, ok := new(big.Int).SetString(tokenID, 10)
	if !ok {
		return "", fmt.Errorf("invalid token ID")
	}
	
	// Call ownerOf(uint256)
	callData := ERC721_OWNER_OF + padLeft(hex.EncodeToString(tokenIDBig.Bytes()), 64)
	result, err := c.ethCall(ctx, contractAddress, callData)
	if err != nil {
		return "", err
	}
	
	if len(result) >= 64 {
		// Extract address from result
		return "0x" + result[len(result)-40:], nil
	}
	
	return "", fmt.Errorf("failed to decode owner address")
}

// ethCall makes a contract call using eth_call
func (c *Client) ethCall(ctx context.Context, to, data string) (string, error) {
	params := []interface{}{
		map[string]interface{}{
			"to":   to,
			"data": data,
		},
		"latest",
	}
	
	result, err := c.call(ctx, "eth_call", params)
	if err != nil {
		return "", err
	}
	
	var hexResult string
	if err := json.Unmarshal(result, &hexResult); err != nil {
		return "", err
	}
	
	return hexResult, nil
}

// getCode fetches contract bytecode
func (c *Client) getCode(ctx context.Context, address string) (string, error) {
	result, err := c.call(ctx, "eth_getCode", []string{address, "latest"})
	if err != nil {
		return "", err
	}
	
	var code string
	if err := json.Unmarshal(result, &code); err != nil {
		return "", err
	}
	
	return code, nil
}

// decodeString decodes a hex string from contract call result
func (c *Client) decodeString(hexData string) string {
	if len(hexData) < 2 {
		return ""
	}
	
	data := hexData[2:] // Remove 0x
	if len(data) < 128 {
		return ""
	}
	
	// Skip offset (first 32 bytes) and length (next 32 bytes)
	lengthHex := data[64:128]
	length, err := strconv.ParseInt(lengthHex, 16, 64)
	if err != nil || length <= 0 {
		return ""
	}
	
	// Extract string data
	if len(data) < 128+int(length)*2 {
		return ""
	}
	
	stringHex := data[128 : 128+int(length)*2]
	stringBytes, err := hex.DecodeString(stringHex)
	if err != nil {
		return ""
	}
	
	return string(stringBytes)
}

// decodeUint256 decodes a uint256 from contract call result
func (c *Client) decodeUint256(hexData string) *big.Int {
	if len(hexData) < 2 {
		return nil
	}
	
	data := hexData[2:] // Remove 0x
	if len(data) != 64 {
		return nil
	}
	
	value, ok := new(big.Int).SetString(data, 16)
	if !ok {
		return nil
	}
	
	return value
}

// padLeft pads a hex string to the specified length
func padLeft(str string, length int) string {
	for len(str) < length {
		str = "0" + str
	}
	return str
}

// GetMultipleTokenBalances fetches balances for multiple tokens
func (c *Client) GetMultipleTokenBalances(ctx context.Context, walletAddress string, tokenContracts []string) ([]*TokenBalance, error) {
	var balances []*TokenBalance
	
	for _, contract := range tokenContracts {
		balance, err := c.GetTokenBalance(ctx, contract, walletAddress)
		if err != nil {
			continue // Skip tokens that fail
		}
		balances = append(balances, balance)
	}
	
	return balances, nil
} 