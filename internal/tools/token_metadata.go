package tools

import (
	"context"
	"fmt"
	"math/big"
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
	return []string{"log_decoder"} // Needs decoded events to find token addresses
}

// Process enriches the baggage with token metadata
func (t *TokenMetadataEnricher) Process(ctx context.Context, baggage map[string]interface{}) error {
	// Extract token addresses from decoded events
	tokenAddresses := t.extractTokenAddresses(baggage)

	if len(tokenAddresses) == 0 {
		return nil // No tokens to enrich
	}

	// Create metadata map
	tokenMetadata := make(map[string]*TokenMetadata)
	
	// Debug: log discovered token addresses
	var discoveredAddresses []string
	for addr := range tokenAddresses {
		discoveredAddresses = append(discoveredAddresses, addr)
	}

	// First, try to extract metadata from event parameters (this is often already available)
	eventExtractedCount := 0
	t.extractMetadataFromEvents(baggage, tokenMetadata)
	for _, metadata := range tokenMetadata {
		if metadata.Symbol != "" || metadata.Name != "" {
			eventExtractedCount++
		}
	}

	// Then, fetch metadata via RPC for any tokens we don't have metadata for
	var rpcErrors []string
	var rpcResults []string
	rpcAttempts := 0
	if t.rpcClient != nil {
		for address := range tokenAddresses {
			addressLower := strings.ToLower(address)
			rpcAttempts++
			
			// Skip if we already have complete metadata from events
			if existing, exists := tokenMetadata[addressLower]; exists && 
				existing.Symbol != "" && existing.Name != "" && existing.Decimals > 0 {
				rpcResults = append(rpcResults, fmt.Sprintf("%s: skipped_complete_metadata", address))
				continue
			}

			rpcResults = append(rpcResults, fmt.Sprintf("%s: attempting_rpc", address))
			if metadata, err := t.fetchTokenMetadata(ctx, address); err == nil {
				rpcResults = append(rpcResults, fmt.Sprintf("%s: rpc_success(name=%s,symbol=%s,decimals=%d)", 
					address, metadata.Name, metadata.Symbol, metadata.Decimals))
				
				// Merge with existing metadata if any, preferring RPC data
				if existing, exists := tokenMetadata[addressLower]; exists {
					if metadata.Name != "" {
						existing.Name = metadata.Name
					}
					if metadata.Symbol != "" {
						existing.Symbol = metadata.Symbol
					}
					if metadata.Decimals >= 0 { // Accept 0 decimals
						existing.Decimals = metadata.Decimals
					}
					if metadata.Type != "" && metadata.Type != "Unknown" {
						existing.Type = metadata.Type
					}
					rpcResults = append(rpcResults, fmt.Sprintf("%s: merged_with_existing", address))
				} else {
					tokenMetadata[addressLower] = metadata
					rpcResults = append(rpcResults, fmt.Sprintf("%s: created_new_metadata", address))
				}
			} else {
				rpcErrors = append(rpcErrors, fmt.Sprintf("%s: %v", address, err))
				rpcResults = append(rpcResults, fmt.Sprintf("%s: rpc_failed", address))
			}
		}
	} else {
		rpcErrors = append(rpcErrors, "RPC client not available")
	}

	// For any tokens we still don't have complete metadata, try to infer from transaction data
	inferenceCount := 0
	for address := range tokenAddresses {
		addressLower := strings.ToLower(address)
		metadata := tokenMetadata[addressLower]
		
		// Create minimal metadata if none exists
		if metadata == nil {
			metadata = &TokenMetadata{
				Address: address,
				Type:    "ERC20", // Assume ERC20 if found in token transfers
				Decimals: 18,     // Safe default
			}
			tokenMetadata[addressLower] = metadata
			inferenceCount++
		}
		
		// Try to improve metadata with smarter inference
		t.improveMetadataWithInference(baggage, addressLower, metadata)
	}

	// Comprehensive debug information
	debugInfo := map[string]interface{}{
		"discovered_addresses":     discoveredAddresses,
		"total_addresses":          len(tokenAddresses),
		"event_extracted_count":    eventExtractedCount,
		"rpc_attempts":            rpcAttempts,
		"rpc_results":             rpcResults,
		"inference_created_count": inferenceCount,
		"final_metadata_count":    len(tokenMetadata),
	}
	
	// Add final metadata summary for debugging
	var finalSummary []string
	for address, metadata := range tokenMetadata {
		finalSummary = append(finalSummary, fmt.Sprintf("%s: name=%q symbol=%q decimals=%d type=%s", 
			address, metadata.Name, metadata.Symbol, metadata.Decimals, metadata.Type))
	}
	debugInfo["final_metadata"] = finalSummary

	// Add RPC errors if any
	if len(rpcErrors) > 0 {
		debugInfo["token_metadata_rpc_errors"] = rpcErrors
	}

	// Store debug info
	if existingDebug, ok := baggage["debug_info"].(map[string]interface{}); ok {
		existingDebug["token_metadata"] = debugInfo
	} else {
		baggage["debug_info"] = map[string]interface{}{
			"token_metadata": debugInfo,
		}
	}

	// Add to baggage
	baggage["token_metadata"] = tokenMetadata

	return nil
}

// extractMetadataFromEvents extracts token metadata from event parameters
func (t *TokenMetadataEnricher) extractMetadataFromEvents(baggage map[string]interface{}, tokenMetadata map[string]*TokenMetadata) {
	// Look for events in baggage
	if eventsInterface, ok := baggage["events"]; ok {
		if eventsList, ok := eventsInterface.([]models.Event); ok {
			for _, event := range eventsList {
				if event.Parameters != nil && event.Contract != "" {
					// Check if this event has token metadata
					contractName, hasName := event.Parameters["contract_name"].(string)
					contractSymbol, hasSymbol := event.Parameters["contract_symbol"].(string)
					contractType, hasType := event.Parameters["contract_type"].(string)

					if hasName || hasSymbol || hasType {
						address := strings.ToLower(event.Contract)

						// Initialize metadata if not exists
						if tokenMetadata[address] == nil {
							tokenMetadata[address] = &TokenMetadata{
								Address: event.Contract,
								Type:    "Unknown",
							}
						}

						// Fill in the metadata from event parameters
						if hasName && contractName != "" {
							tokenMetadata[address].Name = contractName
						}
						if hasSymbol && contractSymbol != "" {
							tokenMetadata[address].Symbol = contractSymbol
						}
						if hasType && contractType != "" {
							tokenMetadata[address].Type = contractType
						}

						// Try to extract decimals from value_decimal in Transfer events
						if event.Name == "Transfer" {
							if valueDecimal, ok := event.Parameters["value_decimal"].(uint64); ok {
								if valueHex, ok := event.Parameters["value"].(string); ok {
									// Infer decimals from the relationship between hex value and decimal value
									decimals := t.inferDecimals(valueHex, valueDecimal, contractSymbol)
									if decimals > 0 {
										tokenMetadata[address].Decimals = decimals
									}
								}
							}
						}
					}
				}
			}
		}
	}
}

// inferDecimals tries to infer token decimals from hex and decimal values
func (t *TokenMetadataEnricher) inferDecimals(valueHex string, valueDecimal uint64, symbol string) int {
	// Generic approach: use RPC data and pattern analysis instead of hardcoding
	// This works for any token without assumptions about specific symbols

	// Try to infer from the hex value
	if strings.HasPrefix(valueHex, "0x") {
		// Convert hex to big int
		hexValue := new(big.Int)
		if _, ok := hexValue.SetString(valueHex[2:], 16); ok {
			// If they're equal, it's likely 0 decimals
			if hexValue.Uint64() == valueDecimal {
				return 0
			}

			// Try common decimal values, starting with most common
			for _, decimals := range []int{18, 6, 8, 9, 12, 4, 2} {
				divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
				
				// Use big.Float for more precise division
				hexFloat := new(big.Float).SetInt(hexValue)
				divisorFloat := new(big.Float).SetInt(divisor)
				result := new(big.Float).Quo(hexFloat, divisorFloat)
				
				// Convert result to uint64 for comparison
				if resultUint64, accuracy := result.Uint64(); accuracy == big.Exact && resultUint64 == valueDecimal {
					return decimals
				}
			}
		}
	}

	// If we can't infer, return 18 as a reasonable default for most ERC20 tokens
	return 18
}

// extractTokenAddresses extracts unique token contract addresses from baggage
func (t *TokenMetadataEnricher) extractTokenAddresses(baggage map[string]interface{}) map[string]bool {
	addresses := make(map[string]bool)

	// Look for events in baggage
	if eventsInterface, ok := baggage["events"]; ok {
		if eventsList, ok := eventsInterface.([]models.Event); ok {
			for _, event := range eventsList {
				if event.Name == "Transfer" && event.Contract != "" {
					// This is likely a token transfer
					addresses[strings.ToLower(event.Contract)] = true
				}
			}
		}
	}

	// Also look for calls that might involve tokens
	if callsInterface, ok := baggage["calls"]; ok {
		if callsList, ok := callsInterface.([]models.Call); ok {
			for _, call := range callsList {
				if call.Contract != "" && t.isLikelyTokenMethod(call.Method) {
					addresses[strings.ToLower(call.Contract)] = true
				}
			}
		}
	}

	return addresses
}

// isLikelyTokenMethod checks if a method name suggests token operations
func (t *TokenMetadataEnricher) isLikelyTokenMethod(method string) bool {
	tokenMethods := []string{
		"transfer", "transferFrom", "approve", "mint", "burn",
		"0xa9059cbb", "0x23b872dd", "0x095ea7b3", // Common ERC20 signatures
	}

	method = strings.ToLower(method)
	for _, tokenMethod := range tokenMethods {
		if strings.Contains(method, tokenMethod) {
			return true
		}
	}
	return false
}

// fetchTokenMetadata fetches metadata for a single token
func (t *TokenMetadataEnricher) fetchTokenMetadata(ctx context.Context, address string) (*TokenMetadata, error) {
	contractInfo, err := t.rpcClient.GetContractInfo(ctx, address)
	if err != nil {
		return nil, fmt.Errorf("GetContractInfo failed: %w", err)
	}

	// Default decimals to 18 if RPC returns 0 (common issue)
	decimals := contractInfo.Decimals
	if decimals == 0 {
		// Only use 18 as default for ERC20 tokens, keep 0 for NFTs
		if contractInfo.Type == "ERC20" || contractInfo.Type == "Unknown" || contractInfo.Type == "" {
			decimals = 18
		}
	}

	metadata := &TokenMetadata{
		Address:  address,
		Name:     contractInfo.Name,
		Symbol:   contractInfo.Symbol,
		Decimals: decimals,
		Type:     contractInfo.Type,
	}
	
	// Log the raw RPC debug info if available
	if debugInfo, ok := contractInfo.Metadata["rpc_debug"].(string); ok {
		metadata.Address = fmt.Sprintf("%s (RPC: %s)", address, debugInfo)
	}

	return metadata, nil
}

// improveMetadataWithInference tries to improve token metadata using transaction data analysis
func (t *TokenMetadataEnricher) improveMetadataWithInference(baggage map[string]interface{}, address string, metadata *TokenMetadata) {
	// Look for Transfer events from this contract to infer decimals more accurately
	if eventsInterface, ok := baggage["events"]; ok {
		if eventsList, ok := eventsInterface.([]models.Event); ok {
			for _, event := range eventsList {
				if strings.EqualFold(event.Contract, address) && event.Name == "Transfer" {
					if event.Parameters != nil {
						// Only infer decimals if we have no decimals or have 0 decimals, but don't override 18 unless we're confident
						if metadata.Decimals == 0 {
							// Try to infer decimals from value patterns
							if valueHex, ok := event.Parameters["value"].(string); ok {
								if valueDecimal, ok := event.Parameters["value_decimal"].(uint64); ok {
									inferredDecimals := t.inferDecimals(valueHex, valueDecimal, metadata.Symbol)
									if inferredDecimals >= 0 && inferredDecimals <= 30 {
										// Only accept inference if it's a common decimal count or if we had 0
										commonDecimals := map[int]bool{0: true, 6: true, 8: true, 9: true, 12: true, 18: true}
										if commonDecimals[inferredDecimals] {
											metadata.Decimals = inferredDecimals
										}
									}
								}
							}
						}
						
						// Use contract metadata if available from RPC calls (only if not already set)
						if contractSymbol, ok := event.Parameters["contract_symbol"].(string); ok && contractSymbol != "" && metadata.Symbol == "" {
							metadata.Symbol = contractSymbol
						}
						if contractName, ok := event.Parameters["contract_name"].(string); ok && contractName != "" && metadata.Name == "" {
							metadata.Name = contractName
						}
					}
					// Only need one Transfer event for inference
					break
				}
			}
		}
	}
}

// GetPromptContext provides token metadata context for LLM prompts
func (t *TokenMetadataEnricher) GetPromptContext(ctx context.Context, baggage map[string]interface{}) string {
	// Get token metadata from baggage
	tokenMetadata, ok := baggage["token_metadata"].(map[string]*TokenMetadata)
	if !ok || len(tokenMetadata) == 0 {
		return ""
	}

	// Build context string from metadata
	var contextParts []string
	for _, metadata := range tokenMetadata {
		if metadata.Type == "ERC20" {
			contextParts = append(contextParts, fmt.Sprintf("- %s (%s): %s with %d decimals", metadata.Name, metadata.Symbol, metadata.Type, metadata.Decimals))
		}
	}

	if len(contextParts) == 0 {
		return ""
	}

	return "Token Metadata:\n" + strings.Join(contextParts, "\n")
}

// GetAnnotationContext provides annotation context for tokens
func (t *TokenMetadataEnricher) GetAnnotationContext(ctx context.Context, baggage map[string]interface{}) *models.AnnotationContext {
	annotationContext := &models.AnnotationContext{
		Items: make([]models.AnnotationContextItem, 0),
	}

	// Get token metadata from baggage
	tokenMetadata, ok := baggage["token_metadata"].(map[string]*TokenMetadata)
	if !ok || len(tokenMetadata) == 0 {
		return annotationContext
	}

	// Add token annotation context
	for _, metadata := range tokenMetadata {
		if metadata.Address == "" {
			continue
		}

		// Create token context item
		description := fmt.Sprintf("%s token", metadata.Type)
		if metadata.Decimals > 0 {
			description += fmt.Sprintf(" with %d decimals", metadata.Decimals)
		}

		annotationContext.AddToken(
			metadata.Address,
			metadata.Symbol,
			metadata.Name,
			"", // Icon will be added by static context provider or from external API
			description,
			map[string]interface{}{
				"decimals": metadata.Decimals,
				"type":     metadata.Type,
			},
		)

		// Also add by symbol for easier matching
		if metadata.Symbol != "" {
			annotationContext.AddItem(models.AnnotationContextItem{
				Type:  "token",
				Value: metadata.Symbol,
				Name:  fmt.Sprintf("%s (%s)", metadata.Name, metadata.Symbol),
				Description: description,
				Metadata: map[string]interface{}{
					"address":  metadata.Address,
					"decimals": metadata.Decimals,
					"type":     metadata.Type,
				},
			})
		}
	}

	return annotationContext
}

// GetTokenMetadata is a helper function to get token metadata from baggage
func GetTokenMetadata(baggage map[string]interface{}, address string) (*TokenMetadata, bool) {
	if metadataMap, ok := baggage["token_metadata"].(map[string]*TokenMetadata); ok {
		metadata, exists := metadataMap[strings.ToLower(address)]
		return metadata, exists
	}
	return nil, false
}

// DebugTokenContract is a utility function for debugging individual token contracts
func (t *TokenMetadataEnricher) DebugTokenContract(ctx context.Context, contractAddress string) map[string]interface{} {
	if t.rpcClient == nil {
		return map[string]interface{}{
			"error": "RPC client not available",
		}
	}
	
	result := map[string]interface{}{
		"contract_address": contractAddress,
	}
	
	// Try to get contract info via RPC
	contractInfo, err := t.rpcClient.GetContractInfo(ctx, contractAddress)
	if err != nil {
		result["rpc_error"] = err.Error()
		return result
	}
	
	result["rpc_success"] = true
	result["name"] = contractInfo.Name
	result["symbol"] = contractInfo.Symbol  
	result["decimals"] = contractInfo.Decimals
	result["type"] = contractInfo.Type
	result["total_supply"] = contractInfo.TotalSupply
	
	// Include debug info from RPC calls
	if debugInfo, ok := contractInfo.Metadata["rpc_debug"].(string); ok {
		result["rpc_debug"] = debugInfo
	}
	
	return result
}
