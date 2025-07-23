package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/txplain/txplain/internal/rpc"
)

// TokenMetadataEnricher enriches ERC20 token addresses with metadata
type TokenMetadataEnricher struct {
	rpcClient *rpc.Client
}

// TokenMetadata represents metadata for a token
type TokenMetadata struct {
	Address  string `json:"address"`
	Name     string `json:"name"`
	Symbol   string `json:"symbol"`
	Decimals int    `json:"decimals"`
	Type     string `json:"type"` // ERC20, ERC721, etc.
}

// NewTokenMetadataEnricher creates a new token metadata enricher
func NewTokenMetadataEnricher() *TokenMetadataEnricher {
	return &TokenMetadataEnricher{
		rpcClient: nil, // Set by SetRPCClient when needed
	}
}

// SetRPCClient sets the RPC client for network-specific operations
func (t *TokenMetadataEnricher) SetRPCClient(client *rpc.Client) {
	t.rpcClient = client
}

// Name returns the processor name
func (t *TokenMetadataEnricher) Name() string {
	return "token_metadata_enricher"
}

// Description returns the processor description
func (t *TokenMetadataEnricher) Description() string {
	return "Enriches ERC20 token addresses with metadata (name, symbol, decimals)"
}

// Dependencies returns the tools this processor depends on
func (t *TokenMetadataEnricher) Dependencies() []string {
	return []string{"abi_resolver"} // Needs contract addresses from ABI resolver
}

// Process enriches the baggage with token metadata by calling individual contract methods
func (t *TokenMetadataEnricher) Process(ctx context.Context, baggage map[string]interface{}) error {
	// Get contract addresses discovered by ABI resolver
	contractAddresses, ok := baggage["contract_addresses"].([]string)
	if !ok || len(contractAddresses) == 0 {
		return nil // No contracts to check
	}

	// Get resolved contracts data from ABI resolver for additional context
	resolvedContracts, _ := baggage["resolved_contracts"].(map[string]*ContractInfo)

	// Create comprehensive contract information map
	contractMetadata := make(map[string]*TokenMetadata)
	allContractInfo := make(map[string]map[string]interface{})
	var debugInfo []string

	// Test each contract address individually
	for _, address := range contractAddresses {
		contractInfo := make(map[string]interface{})
		hasAnyTokenLikeData := false

		// Get ABI resolver data first (this is the most authoritative)
		var abiContract *ContractInfo
		if resolvedContracts != nil {
			abiContract, _ = resolvedContracts[strings.ToLower(address)]
		}

		// Add ABI resolver information (contract name from verification, etc.)
		if abiContract != nil && abiContract.IsVerified {
			if abiContract.ContractName != "" {
				contractInfo["verified_name"] = abiContract.ContractName
				debugInfo = append(debugInfo, fmt.Sprintf("%s: verified_name=%s", address, abiContract.ContractName))
			}
			if abiContract.CompilerVersion != "" {
				contractInfo["compiler_version"] = abiContract.CompilerVersion
			}
			if abiContract.IsProxy {
				contractInfo["is_proxy"] = true
				if abiContract.Implementation != "" {
					contractInfo["implementation"] = abiContract.Implementation
				}
			}
			contractInfo["is_verified"] = true
			contractInfo["source_verified"] = true
		}

		// Get RPC contract information (method calls, etc.)
		rpcInfo, err := t.getRPCContractInfo(ctx, address)
		if err != nil {
			debugInfo = append(debugInfo, fmt.Sprintf("%s: RPC_ERROR=%v", address, err))
		} else {
			// Extract RPC information
			if rpcInfo.Name != "" {
				contractInfo["rpc_name"] = rpcInfo.Name
				hasAnyTokenLikeData = true
				debugInfo = append(debugInfo, fmt.Sprintf("%s: rpc_name=%s", address, rpcInfo.Name))
			}

			if rpcInfo.Symbol != "" {
				contractInfo["rpc_symbol"] = rpcInfo.Symbol
				hasAnyTokenLikeData = true
				debugInfo = append(debugInfo, fmt.Sprintf("%s: rpc_symbol=%s", address, rpcInfo.Symbol))
			}

			if rpcInfo.Decimals >= 0 {
				contractInfo["rpc_decimals"] = rpcInfo.Decimals
				hasAnyTokenLikeData = true
				debugInfo = append(debugInfo, fmt.Sprintf("%s: rpc_decimals=%d", address, rpcInfo.Decimals))
			}

			if rpcInfo.TotalSupply != "" && rpcInfo.TotalSupply != "0" {
				contractInfo["rpc_total_supply"] = rpcInfo.TotalSupply
				hasAnyTokenLikeData = true
			}

			// Add supported interfaces from RPC metadata
			if supportedInterfaces, ok := rpcInfo.Metadata["supported_interfaces"].([]string); ok && len(supportedInterfaces) > 0 {
				contractInfo["supported_interfaces"] = supportedInterfaces
				debugInfo = append(debugInfo, fmt.Sprintf("%s: supported_interfaces=%v", address, supportedInterfaces))
			}

			// Add available methods from RPC metadata
			if availableMethods, ok := rpcInfo.Metadata["available_methods"].([]string); ok && len(availableMethods) > 0 {
				contractInfo["available_methods"] = availableMethods
				debugInfo = append(debugInfo, fmt.Sprintf("%s: available_methods=%v", address, availableMethods))
			}

			// Add RPC debug info
			if rpcDebug, ok := rpcInfo.Metadata["rpc_debug"].(string); ok {
				contractInfo["rpc_debug"] = rpcDebug
			}
		}

		// Determine the best name and symbol to use (prioritize verified data)
		var bestName, bestSymbol string
		var bestDecimals int = -1

		// Priority: verified ABI name > RPC name
		if verifiedName, ok := contractInfo["verified_name"].(string); ok && verifiedName != "" {
			bestName = verifiedName
		} else if rpcName, ok := contractInfo["rpc_name"].(string); ok && rpcName != "" {
			bestName = rpcName
		}

		// For symbol and decimals, use RPC data since verified contracts might not have these in the name
		if rpcSymbol, ok := contractInfo["rpc_symbol"].(string); ok && rpcSymbol != "" {
			bestSymbol = rpcSymbol
		}
		if rpcDecimals, ok := contractInfo["rpc_decimals"].(int); ok {
			bestDecimals = rpcDecimals
		}

		// Determine token type based on available methods and responses
		tokenType := "Contract"
		if bestName != "" || bestSymbol != "" {
			if bestDecimals > 0 {
				tokenType = "ERC20" // Has name/symbol and decimals - this is an ERC20 token
			} else {
				tokenType = "ERC721" // Has name/symbol but no decimals - likely ERC721
			}
		}

		// Store all discovered information
		allContractInfo[address] = contractInfo

		// Create TokenMetadata for contracts that have token-like characteristics
		// Use proper token type classification based on available data
		if hasAnyTokenLikeData || (abiContract != nil && abiContract.IsVerified) {
			metadata := &TokenMetadata{
				Address:  address,
				Type:     tokenType, // Use determined token type instead of generic "Contract"
				Name:     bestName,
				Symbol:   bestSymbol,
				Decimals: bestDecimals,
			}
			contractMetadata[address] = metadata
		}
	}

	// Add to baggage
	if len(contractMetadata) > 0 {
		baggage["token_metadata"] = contractMetadata
	}

	// Only store debug information in DEBUG mode to avoid overwhelming baggage
	if os.Getenv("DEBUG") == "true" {
		// Add all contract information to baggage for debug purposes only
		if len(allContractInfo) > 0 {
			baggage["all_contract_info"] = allContractInfo
		}

		// Add debug information only in DEBUG mode
		if len(debugInfo) > 0 {
			if existingDebug, ok := baggage["debug_info"].(map[string]interface{}); ok {
				existingDebug["token_metadata_enricher"] = debugInfo
			} else {
				baggage["debug_info"] = map[string]interface{}{
					"token_metadata_enricher": debugInfo,
				}
			}
		}
	}

	return nil
}

// Helper functions
func getStringValue(info map[string]interface{}, key string) string {
	if val, ok := info[key].(string); ok {
		return val
	}
	return ""
}

// GetPromptContext provides context for the LLM prompt
func (t *TokenMetadataEnricher) GetPromptContext(ctx context.Context, baggage map[string]interface{}) string {
	var contextParts []string

	// Add comprehensive contract information
	if allContractInfo, ok := baggage["all_contract_info"].(map[string]map[string]interface{}); ok && len(allContractInfo) > 0 {
		contextParts = append(contextParts, "=== CONTRACT INFORMATION ===")
		for address, info := range allContractInfo {
			var contractDesc []string
			contractDesc = append(contractDesc, fmt.Sprintf("Contract: %s", address))

			// Verified contract information (most authoritative)
			if verifiedName, ok := info["verified_name"].(string); ok && verifiedName != "" {
				contractDesc = append(contractDesc, fmt.Sprintf("Verified Name: %s", verifiedName))
			}
			if isVerified, ok := info["is_verified"].(bool); ok && isVerified {
				contractDesc = append(contractDesc, "Source: Verified on Etherscan")
			}
			if isProxy, ok := info["is_proxy"].(bool); ok && isProxy {
				contractDesc = append(contractDesc, "Type: Proxy Contract")
				if impl, ok := info["implementation"].(string); ok && impl != "" {
					contractDesc = append(contractDesc, fmt.Sprintf("Implementation: %s", impl))
				}
			}

			// RPC method call results
			if rpcName, ok := info["rpc_name"].(string); ok && rpcName != "" {
				contractDesc = append(contractDesc, fmt.Sprintf("Name() method returns: %s", rpcName))
			}
			if rpcSymbol, ok := info["rpc_symbol"].(string); ok && rpcSymbol != "" {
				contractDesc = append(contractDesc, fmt.Sprintf("Symbol() method returns: %s", rpcSymbol))
			}
			if rpcDecimals, ok := info["rpc_decimals"].(int); ok && rpcDecimals >= 0 {
				contractDesc = append(contractDesc, fmt.Sprintf("Decimals() method returns: %d", rpcDecimals))
			}
			if totalSupply, ok := info["rpc_total_supply"].(string); ok && totalSupply != "" {
				contractDesc = append(contractDesc, fmt.Sprintf("TotalSupply() method returns: %s", totalSupply))
			}

			// Supported interfaces
			if interfaces, ok := info["supported_interfaces"].([]string); ok && len(interfaces) > 0 {
				contractDesc = append(contractDesc, fmt.Sprintf("Supported Interfaces: %v", interfaces))
			}

			// Available methods
			if methods, ok := info["available_methods"].([]string); ok && len(methods) > 0 {
				contractDesc = append(contractDesc, fmt.Sprintf("Available Methods: %v", methods))
			}

			// Compiler info for verified contracts
			if compiler, ok := info["compiler_version"].(string); ok && compiler != "" {
				contractDesc = append(contractDesc, fmt.Sprintf("Compiler: %s", compiler))
			}

			// Add to context
			contextParts = append(contextParts, "- "+strings.Join(contractDesc, "\n  "))
		}
	}

	// Add simplified token metadata summary (if any contracts have token-like characteristics)
	if tokenMetadata, ok := baggage["token_metadata"].(map[string]*TokenMetadata); ok && len(tokenMetadata) > 0 {
		contextParts = append(contextParts, "", "=== CONTRACTS WITH TOKEN-LIKE METHODS ===")
		for address, metadata := range tokenMetadata {
			line := fmt.Sprintf("- %s", address)
			if metadata.Name != "" {
				line += fmt.Sprintf(": %s", metadata.Name)
			}
			if metadata.Symbol != "" {
				line += fmt.Sprintf(" (%s)", metadata.Symbol)
			}
			if metadata.Decimals >= 0 {
				line += fmt.Sprintf(" - %d decimals", metadata.Decimals)
			}
			contextParts = append(contextParts, line)
		}

		contextParts = append(contextParts, "", "Note: These contracts respond to token-like methods (name, symbol, decimals) but may not be actual tokens. Router contracts, aggregators, and other DeFi contracts often implement these methods for compatibility.")
	}

	return strings.Join(contextParts, "\n")
}

// getRPCContractInfo calls GetContractInfo once and returns the result
func (t *TokenMetadataEnricher) getRPCContractInfo(ctx context.Context, address string) (*rpc.ContractInfo, error) {
	if t.rpcClient == nil {
		return nil, fmt.Errorf("no RPC client available")
	}

	return t.rpcClient.GetContractInfo(ctx, address)
}

// GetRagContext provides RAG context for token metadata information
func (t *TokenMetadataEnricher) GetRagContext(ctx context.Context, baggage map[string]interface{}) *RagContext {
	ragContext := NewRagContext()
	
	tokenMetadata, ok := baggage["token_metadata"].(map[string]*TokenMetadata)
	if !ok || len(tokenMetadata) == 0 {
		return ragContext
	}

	// Add token metadata to RAG context for searchability
	for address, metadata := range tokenMetadata {
		if metadata.Name != "" && metadata.Symbol != "" {
			ragContext.AddItem(RagContextItem{
				ID:      fmt.Sprintf("token_%s", address),
				Type:    "token",
				Title:   fmt.Sprintf("%s (%s) Token", metadata.Name, metadata.Symbol),
				Content: fmt.Sprintf("Token %s (%s) at address %s has %d decimals and is of type %s", metadata.Name, metadata.Symbol, address, metadata.Decimals, metadata.Type),
				Metadata: map[string]interface{}{
					"address":  address,
					"name":     metadata.Name,
					"symbol":   metadata.Symbol,
					"decimals": metadata.Decimals,
					"type":     metadata.Type,
				},
				Keywords:  []string{metadata.Name, metadata.Symbol, address, "token", metadata.Type},
				Relevance: 0.8,
			})
		}
	}

	return ragContext
}
