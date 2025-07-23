package rpc

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// ContractInfo represents information about a contract
type ContractInfo struct {
	Address     string                 `json:"address"`
	Type        string                 `json:"type"` // ERC20, ERC721, ERC1155, Unknown
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
	ERC721_NAME               = "0x06fdde03" // name()
	ERC721_SYMBOL             = "0x95d89b41" // symbol()
	ERC721_TOKEN_URI          = "0xc87b56dd" // tokenURI(uint256)
	ERC721_OWNER_OF           = "0x6352211e" // ownerOf(uint256)
	ERC721_BALANCE_OF         = "0x70a08231" // balanceOf(address)
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
		Type:     "Contract", // Generic type - let LLM classify
		Metadata: make(map[string]interface{}),
	}

	// Check if it's a contract (has code)
	code, err := c.getCode(ctx, contractAddress)
	if err != nil || code == "0x" || code == "" {
		return info, nil // Not a contract
	}

	// Store available interface information without classifying
	supportedInterfaces := c.checkSupportedInterfaces(ctx, contractAddress)
	if len(supportedInterfaces) > 0 {
		info.Metadata["supported_interfaces"] = supportedInterfaces
	}

	// Try to fetch available method information without assuming contract type
	availableMethods := c.checkAvailableMethods(ctx, contractAddress)
	if len(availableMethods) > 0 {
		info.Metadata["available_methods"] = availableMethods
	}

	// Try to fetch standard token methods if available (but don't classify as token)
	c.tryFetchTokenLikeInfo(ctx, contractAddress, info)

	return info, nil
}

// checkSupportedInterfaces checks which standard interfaces are supported
func (c *Client) checkSupportedInterfaces(ctx context.Context, contractAddress string) []string {
	var interfaces []string

	// Check common interfaces
	interfaceChecks := map[string]string{
		"ERC165":  ERC165_INTERFACE_ID,
		"ERC20":   ERC20_INTERFACE_ID,
		"ERC721":  ERC721_INTERFACE_ID,
		"ERC1155": ERC1155_INTERFACE_ID,
	}

	for name, id := range interfaceChecks {
		if c.supportsInterface(ctx, contractAddress, id) {
			interfaces = append(interfaces, name)
		}
	}

	return interfaces
}

// checkAvailableMethods checks which standard methods are available
func (c *Client) checkAvailableMethods(ctx context.Context, contractAddress string) []string {
	var methods []string

	// Check common ERC20 methods
	erc20Methods := map[string]string{
		"name":        ERC20_NAME,
		"symbol":      ERC20_SYMBOL,
		"decimals":    ERC20_DECIMALS,
		"totalSupply": ERC20_TOTAL_SUPPLY,
		"balanceOf":   ERC20_BALANCE_OF,
	}

	for methodName, signature := range erc20Methods {
		if c.methodExists(ctx, contractAddress, signature) {
			methods = append(methods, methodName)
		}
	}

	// Check common ERC721 methods
	erc721Methods := map[string]string{
		"tokenURI": ERC721_TOKEN_URI,
		"ownerOf":  ERC721_OWNER_OF,
	}

	for methodName, signature := range erc721Methods {
		if c.methodExists(ctx, contractAddress, signature) {
			methods = append(methods, methodName)
		}
	}

	return methods
}

// methodExists checks if a method exists by attempting to call it
func (c *Client) methodExists(ctx context.Context, contractAddress, methodSig string) bool {
	// For methods that require parameters, we'll skip them for now
	// Only check parameter-less methods like name(), symbol(), decimals(), totalSupply()
	if methodSig == ERC20_BALANCE_OF || methodSig == ERC721_OWNER_OF || methodSig == ERC721_TOKEN_URI {
		return false // Skip methods that need parameters
	}

	_, err := c.ethCall(ctx, contractAddress, methodSig)
	return err == nil
}

// tryFetchTokenLikeInfo attempts to fetch token-like information without classification
func (c *Client) tryFetchTokenLikeInfo(ctx context.Context, contractAddress string, info *ContractInfo) {
	// Add debug context
	var debugInfo []string

	// Try to fetch name
	if nameResult, err := c.ethCall(ctx, contractAddress, ERC20_NAME); err == nil {
		if name := c.decodeString(nameResult); name != "" {
			info.Name = name
			debugInfo = append(debugInfo, fmt.Sprintf("name=%s", name))
		} else {
			debugInfo = append(debugInfo, "name=empty_decode")
		}
	} else {
		debugInfo = append(debugInfo, fmt.Sprintf("name_error=%v", err))
	}

	// Try to fetch symbol
	if symbolResult, err := c.ethCall(ctx, contractAddress, ERC20_SYMBOL); err == nil {
		if symbol := c.decodeString(symbolResult); symbol != "" {
			info.Symbol = symbol
			debugInfo = append(debugInfo, fmt.Sprintf("symbol=%s", symbol))
		} else {
			debugInfo = append(debugInfo, "symbol=empty_decode")
		}
	} else {
		debugInfo = append(debugInfo, fmt.Sprintf("symbol_error=%v", err))
	}

	// Try to fetch decimals
	decimalsSet := false
	if decimalsResult, err := c.ethCall(ctx, contractAddress, ERC20_DECIMALS); err == nil {
		if decimals := c.decodeUint256(decimalsResult); decimals != nil {
			fetchedDecimals := int(decimals.Int64())
			// Accept any reasonable decimals value including 0
			if fetchedDecimals >= 0 && fetchedDecimals <= 30 {
				info.Decimals = fetchedDecimals
				decimalsSet = true
				debugInfo = append(debugInfo, fmt.Sprintf("decimals=%d", fetchedDecimals))
			} else {
				debugInfo = append(debugInfo, fmt.Sprintf("decimals_out_of_range=%d", fetchedDecimals))
			}
		} else {
			debugInfo = append(debugInfo, "decimals=decode_failed")
		}
	} else {
		debugInfo = append(debugInfo, fmt.Sprintf("decimals_error=%v", err))
	}

	// Only default to 18 if we couldn't fetch decimals at all and we have other token-like info
	if !decimalsSet && (info.Name != "" || info.Symbol != "") {
		info.Decimals = 18
		debugInfo = append(debugInfo, "decimals=default_18")
	}

	// Try to fetch total supply
	if supplyResult, err := c.ethCall(ctx, contractAddress, ERC20_TOTAL_SUPPLY); err == nil {
		if supply := c.decodeUint256(supplyResult); supply != nil {
			info.TotalSupply = supply.String()
			debugInfo = append(debugInfo, "totalSupply=ok")
		} else {
			debugInfo = append(debugInfo, "totalSupply=decode_failed")
		}
	} else {
		debugInfo = append(debugInfo, fmt.Sprintf("totalSupply_error=%v", err))
	}

	// Store debug info in metadata for troubleshooting
	if len(debugInfo) > 0 {
		info.Metadata["rpc_debug"] = strings.Join(debugInfo, ", ")
	}
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

// These functions are no longer needed as we provide raw information without classification

// GetTokenBalance fetches token balance for an address (assumes ERC20-like interface)
func (c *Client) GetTokenBalance(ctx context.Context, contractAddress, walletAddress string) (*TokenBalance, error) {
	// Get contract info first
	contractInfo, err := c.GetContractInfo(ctx, contractAddress)
	if err != nil {
		return nil, err
	}

	// Try to call balanceOf(address) regardless of detected type
	callData := ERC20_BALANCE_OF + padLeft(walletAddress[2:], 64)
	result, err := c.ethCall(ctx, contractAddress, callData)
	if err != nil {
		return nil, fmt.Errorf("contract does not support balanceOf method: %w", err)
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
