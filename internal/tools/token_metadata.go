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
	Address   string `json:"address"`
	Name      string `json:"name"`
	Symbol    string `json:"symbol"`
	Decimals  int    `json:"decimals"`
	Type      string `json:"type"` // ERC20, ERC721, etc.
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

	// Fetch metadata for each unique token address
	if t.rpcClient != nil {
		for address := range tokenAddresses {
			if metadata, err := t.fetchTokenMetadata(ctx, address); err == nil {
				tokenMetadata[strings.ToLower(address)] = metadata
			}
		}
	}

	// Add to baggage
	baggage["token_metadata"] = tokenMetadata

	return nil
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
		return nil, err
	}

	return &TokenMetadata{
		Address:  address,
		Name:     contractInfo.Name,
		Symbol:   contractInfo.Symbol,
		Decimals: contractInfo.Decimals,
		Type:     contractInfo.Type,
	}, nil
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

// GetTokenMetadata is a helper function to get token metadata from baggage
func GetTokenMetadata(baggage map[string]interface{}, address string) (*TokenMetadata, bool) {
	if metadataMap, ok := baggage["token_metadata"].(map[string]*TokenMetadata); ok {
		metadata, exists := metadataMap[strings.ToLower(address)]
		return metadata, exists
	}
	return nil, false
} 