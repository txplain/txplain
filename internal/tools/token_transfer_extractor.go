package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/txplain/txplain/internal/models"
)

// TokenTransferExtractor extracts token transfers from events
type TokenTransferExtractor struct{}

// NewTokenTransferExtractor creates a new token transfer extractor
func NewTokenTransferExtractor() *TokenTransferExtractor {
	return &TokenTransferExtractor{}
}

// Name returns the tool name
func (t *TokenTransferExtractor) Name() string {
	return "token_transfer_extractor"
}

// Description returns the tool description
func (t *TokenTransferExtractor) Description() string {
	return "Extracts ERC20/ERC721 token transfers from decoded transaction events"
}

// Dependencies returns the tools this processor depends on
func (t *TokenTransferExtractor) Dependencies() []string {
	return []string{"log_decoder"} // Needs decoded events
}

// Process extracts token transfers from events and adds them to baggage
func (t *TokenTransferExtractor) Process(ctx context.Context, baggage map[string]interface{}) error {
	// Get events from baggage
	events, ok := baggage["events"].([]models.Event)
	if !ok || len(events) == 0 {
		return nil // No events to process
	}

	// Extract transfers
	transfers := t.extractTokenTransfers(events)

	// Add transfers to baggage for other tools to use
	baggage["transfers"] = transfers

	return nil
}

// extractTokenTransfers extracts token transfers from events with proper filtering and cleaning
func (t *TokenTransferExtractor) extractTokenTransfers(events []models.Event) []models.TokenTransfer {
	var transfers []models.TokenTransfer

	for _, event := range events {
		// Generic transfer detection: look for events with from/to pattern regardless of event name
		if t.isTokenTransferEvent(event) {
			transfer := models.TokenTransfer{
				Contract: event.Contract,
			}

			// Extract from, to, and amount/tokenId from parameters
			if params := event.Parameters; params != nil {
				// Clean and extract addresses
				if from, ok := params["from"].(string); ok {
					transfer.From = t.cleanAddress(from)
				}
				if to, ok := params["to"].(string); ok {
					transfer.To = t.cleanAddress(to)
				}

				// Check for ERC20-style value parameter
				if value, ok := params["value"].(string); ok {
					transfer.Amount = value
					transfer.Type = "ERC20" // Assume ERC20 for value parameter
				}

				// Check for ERC721-style tokenId parameter
				if tokenId, ok := params["tokenId"].(string); ok {
					transfer.TokenID = tokenId
					transfer.Type = "ERC721" // NFT transfer
				}
			}

			// Skip if we don't have valid from/to addresses
			if transfer.From == "" || transfer.To == "" {
				continue
			}

			// Skip zero-amount ERC20 transfers (but keep ERC721 transfers)
			if transfer.Type == "ERC20" && t.isZeroAmount(transfer.Amount) {
				continue
			}

			// Token metadata will be added later by the metadata enricher
			transfers = append(transfers, transfer)
		}
	}

	// Filter out irrelevant intermediate transfers
	return t.filterRelevantTransfers(transfers)
}

// isTokenTransferEvent detects if an event represents a token transfer based on parameter structure
func (t *TokenTransferExtractor) isTokenTransferEvent(event models.Event) bool {
	if event.Parameters == nil {
		return false
	}

	// Generic pattern: must have 'from' and 'to' parameters
	_, hasFrom := event.Parameters["from"]
	_, hasTo := event.Parameters["to"]

	if !hasFrom || !hasTo {
		return false
	}

	// Must have either a 'value' parameter (ERC20) or 'tokenId' parameter (ERC721)
	_, hasValue := event.Parameters["value"]
	_, hasTokenId := event.Parameters["tokenId"]

	// For ERC1155: might have 'id' and 'value' instead of 'tokenId'
	_, hasId := event.Parameters["id"]

	return hasValue || hasTokenId || hasId
}

// cleanAddress removes padding from addresses and validates format
func (t *TokenTransferExtractor) cleanAddress(address string) string {
	if address == "" {
		return ""
	}

	// Remove 0x prefix for processing
	addr := address
	hasPrefix := strings.HasPrefix(addr, "0x")
	if hasPrefix {
		addr = addr[2:]
	}

	// If it's a padded 64-character address, extract the last 40 characters
	if len(addr) == 64 {
		addr = addr[24:] // Remove padding, keep last 40 chars
	}

	// Validate it's a proper hex address (40 characters)
	if len(addr) != 40 {
		return ""
	}

	// Re-add prefix and return
	return "0x" + addr
}

// isZeroAmount checks if an amount string represents zero
func (t *TokenTransferExtractor) isZeroAmount(amount string) bool {
	if amount == "" || amount == "0x" || amount == "0x0" {
		return true
	}

	// Remove 0x prefix
	if strings.HasPrefix(amount, "0x") {
		amount = amount[2:]
	}

	// Check if all characters are zeros
	for _, char := range amount {
		if char != '0' {
			return false
		}
	}

	return true
}

// filterRelevantTransfers removes irrelevant intermediate transfers (like WETH in multi-hop swaps)
func (t *TokenTransferExtractor) filterRelevantTransfers(transfers []models.TokenTransfer) []models.TokenTransfer {
	if len(transfers) <= 2 {
		return transfers // Keep simple transactions as-is
	}

	var filtered []models.TokenTransfer

	// Group transfers by contract to identify patterns
	contractTransfers := make(map[string][]models.TokenTransfer)
	for _, transfer := range transfers {
		contractTransfers[transfer.Contract] = append(contractTransfers[transfer.Contract], transfer)
	}

	// Generic intermediate token filtering based on transfer patterns
	// Works for any network without hardcoding specific addresses

	for contract, contractTransferList := range contractTransfers {
		// Generic pattern detection: identify intermediate tokens by transfer patterns
		// This is more generic than hardcoding specific addresses
		mightBeIntermediate := len(contractTransferList) >= 2

		if mightBeIntermediate {
			// Check if there are other token transfers (indicating this might be intermediate)
			hasOtherTokenTransfers := false
			for otherContract := range contractTransfers {
				if otherContract != contract && len(contractTransfers[otherContract]) > 0 {
					hasOtherTokenTransfers = true
					break
				}
			}

			// Skip intermediate transfers if there are other token transfers
			if hasOtherTokenTransfers {
				continue
			}
		}

		// Add non-intermediate transfers
		filtered = append(filtered, contractTransferList...)
	}

	return filtered
}

// GetPromptContext provides transfer context for LLM prompts
func (t *TokenTransferExtractor) GetPromptContext(ctx context.Context, baggage map[string]interface{}) string {
	// Get transfers from baggage
	transfers, ok := baggage["transfers"].([]models.TokenTransfer)
	if !ok || len(transfers) == 0 {
		return ""
	}

	// Build basic token transfers section for the prompt
	context := "### Basic Token Transfers:"

	for i, transfer := range transfers {
		context += "\n\nTransfer #" + fmt.Sprintf("%d", i+1) + ":"
		context += "\n- Type: " + transfer.Type
		context += "\n- Contract: " + transfer.Contract
		context += "\n- From: " + transfer.From
		context += "\n- To: " + transfer.To

		if transfer.Amount != "" {
			context += "\n- Raw Amount: " + transfer.Amount
		}

		if transfer.TokenID != "" {
			context += "\n- Token ID: " + transfer.TokenID
		}
	}

	return context
}

// GetRagContext provides RAG context for token transfers (minimal for this tool)
func (t *TokenTransferExtractor) GetRagContext(ctx context.Context, baggage map[string]interface{}) *RagContext {
	ragContext := NewRagContext()
	// Token transfer extractor processes transaction-specific transfer data
	// No general knowledge to contribute to RAG
	return ragContext
}
