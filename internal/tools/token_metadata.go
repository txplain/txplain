package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/txplain/txplain/internal/models"
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

	// Create metadata map for contracts with any token-like data
	contractMetadata := make(map[string]*TokenMetadata)
	
	// Track all discovered contract information
	allContractInfo := make(map[string]map[string]interface{})
	var debugInfo []string

	// Test each contract address individually
	for _, address := range contractAddresses {
		contractInfo := make(map[string]interface{})
		hasAnyTokenData := false

		// Call GetContractInfo once per contract to get all available data
		rpcInfo, err := t.getRPCContractInfo(ctx, address)
		if err != nil {
			continue // Skip contracts that can't be queried
		}

		// Extract individual pieces of information from the single RPC call
		name := rpcInfo.Name
		symbol := rpcInfo.Symbol
		decimals := rpcInfo.Decimals
		contractType := rpcInfo.Type
		
		// 1. Process name
		if name != "" {
			contractInfo["name"] = name
			hasAnyTokenData = true
			debugInfo = append(debugInfo, fmt.Sprintf("%s: name=%s", address, name))
		}

		// 2. Process symbol
		if symbol != "" {
			contractInfo["symbol"] = symbol
			hasAnyTokenData = true
			debugInfo = append(debugInfo, fmt.Sprintf("%s: symbol=%s", address, symbol))
		}

		// 3. Process decimals (check if it was actually fetched vs defaulted)
		if decimals >= 0 && (contractType != "ERC20" || decimals != 18) {
			// Either it's not ERC20, or it's ERC20 but not the default 18
			contractInfo["decimals"] = decimals
			hasAnyTokenData = true
			debugInfo = append(debugInfo, fmt.Sprintf("%s: decimals=%d", address, decimals))
		}

		// 4. Process contract type/interfaces
		var interfaces []string
		if contractType != "" && contractType != "Unknown" {
			interfaces = append(interfaces, contractType)
			if contractType == "ERC721" || contractType == "ERC1155" {
				interfaces = append(interfaces, "ERC165") // These typically support ERC165
			}
			contractInfo["interfaces"] = interfaces
			contractInfo["type"] = contractType
			hasAnyTokenData = true
			debugInfo = append(debugInfo, fmt.Sprintf("%s: type=%s interfaces=%v", address, contractType, interfaces))
		}

		// Store all discovered information
		allContractInfo[address] = contractInfo

		// If we found any token-like data, create TokenMetadata
		if hasAnyTokenData {
			metadata := &TokenMetadata{
				Address:  address,
				Type:     contractType,
				Name:     name,
				Symbol:   symbol,
				Decimals: decimals,
			}
			contractMetadata[address] = metadata
		}
	}

	// Add to baggage
	if len(contractMetadata) > 0 {
		baggage["token_metadata"] = contractMetadata
	}

	// Add all contract information to baggage for other tools to use
	baggage["all_contract_info"] = allContractInfo

	// Add debug information
	if len(debugInfo) > 0 {
		if existingDebug, ok := baggage["debug_info"].(map[string]interface{}); ok {
			existingDebug["token_metadata_individual_calls"] = debugInfo
		} else {
			baggage["debug_info"] = map[string]interface{}{
				"token_metadata_individual_calls": debugInfo,
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

func getIntValue(info map[string]interface{}, key string) int {
	if val, ok := info[key].(int); ok {
		return val
	}
	return 0
}

// GetAnnotationContext provides context for annotations
func (t *TokenMetadataEnricher) GetAnnotationContext(ctx context.Context, baggage map[string]interface{}) *models.AnnotationContext {
	annotationContext := &models.AnnotationContext{
		Items: make([]models.AnnotationContextItem, 0),
	}

	// Add token metadata to annotation context
	if tokenMetadata, ok := baggage["token_metadata"].(map[string]*TokenMetadata); ok {
		for address, metadata := range tokenMetadata {
			description := fmt.Sprintf("%s token", metadata.Type)
			if metadata.Decimals > 0 {
				description += fmt.Sprintf(" with %d decimals", metadata.Decimals)
			}

			annotationContext.AddToken(
				address,
				metadata.Symbol,
				metadata.Name,
				"", // Icon will be added by static context provider
				description,
				map[string]interface{}{
					"decimals": metadata.Decimals,
					"type":     metadata.Type,
				},
			)

			// Also add by symbol for easier matching
			if metadata.Symbol != "" {
				annotationContext.AddToken(
					address,
					metadata.Symbol,
					metadata.Name,
					"",
					description,
					map[string]interface{}{
						"address":  address,
						"decimals": metadata.Decimals,
						"type":     metadata.Type,
					},
				)
			}
		}
	}

	// Also add information from all_contract_info for contracts that might not be full tokens
	if allContractInfo, ok := baggage["all_contract_info"].(map[string]map[string]interface{}); ok {
		for address, info := range allContractInfo {
			// Skip if already added as token metadata
			if _, alreadyAdded := baggage["token_metadata"].(map[string]*TokenMetadata)[address]; alreadyAdded {
				continue
			}

			// Add partial contract information
			if name := getStringValue(info, "name"); name != "" {
				annotationContext.AddAddress(
					address,
					fmt.Sprintf("Contract: %s", name),
					"",
					fmt.Sprintf("Contract address for %s", name),
				)
			} else if symbol := getStringValue(info, "symbol"); symbol != "" {
				annotationContext.AddAddress(
					address,
					fmt.Sprintf("Contract with symbol: %s", symbol),
					"",
					fmt.Sprintf("Contract address with symbol %s", symbol),
				)
			}
		}
	}

	return annotationContext
}

// GetPromptContext provides context for the LLM prompt
func (t *TokenMetadataEnricher) GetPromptContext(ctx context.Context, baggage map[string]interface{}) string {
	var contextParts []string

	// Add discovered token metadata
	if tokenMetadata, ok := baggage["token_metadata"].(map[string]*TokenMetadata); ok && len(tokenMetadata) > 0 {
		contextParts = append(contextParts, "=== TOKEN CONTRACTS DISCOVERED ===")
		for address, metadata := range tokenMetadata {
			line := fmt.Sprintf("- %s: %s (%s)", address, metadata.Name, metadata.Symbol)
			if metadata.Type != "" {
				line += fmt.Sprintf(" [%s]", metadata.Type)
			}
			if metadata.Decimals > 0 {
				line += fmt.Sprintf(" - %d decimals", metadata.Decimals)
			}
			contextParts = append(contextParts, line)
		}
	}

	// Add all contract information for partial data
	if allContractInfo, ok := baggage["all_contract_info"].(map[string]map[string]interface{}); ok && len(allContractInfo) > 0 {
		hasPartialInfo := false
		var partialInfoLines []string

		for address, info := range allContractInfo {
			// Skip if already covered in token metadata
			if _, covered := baggage["token_metadata"].(map[string]*TokenMetadata)[address]; covered {
				continue
			}

			var infoParts []string
			if name := getStringValue(info, "name"); name != "" {
				infoParts = append(infoParts, fmt.Sprintf("name=%s", name))
			}
			if symbol := getStringValue(info, "symbol"); symbol != "" {
				infoParts = append(infoParts, fmt.Sprintf("symbol=%s", symbol))
			}
			if interfaces, ok := info["interfaces"].([]string); ok && len(interfaces) > 0 {
				infoParts = append(infoParts, fmt.Sprintf("interfaces=%v", interfaces))
			}

			if len(infoParts) > 0 {
				hasPartialInfo = true
				partialInfoLines = append(partialInfoLines, fmt.Sprintf("- %s: %s", address, strings.Join(infoParts, ", ")))
			}
		}

		if hasPartialInfo {
			contextParts = append(contextParts, "", "=== CONTRACTS WITH PARTIAL INFO ===")
			contextParts = append(contextParts, partialInfoLines...)
		}
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
