package tools

import (
	"context"
	"fmt"
	"math"
	"math/big"
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

// extractTokenTransfers extracts token transfers from events
func (t *TokenTransferExtractor) extractTokenTransfers(events []models.Event) []models.TokenTransfer {
	var transfers []models.TokenTransfer

	for _, event := range events {
		if event.Name == "Transfer" {
			transfer := models.TokenTransfer{
				Contract: event.Contract,
			}

			// Extract from, to, and amount/tokenId from parameters
			if params := event.Parameters; params != nil {
				if from, ok := params["from"].(string); ok {
					transfer.From = from
				}
				if to, ok := params["to"].(string); ok {
					transfer.To = to
				}
				if value, ok := params["value"].(string); ok {
					transfer.Amount = value
					transfer.Type = "ERC20" // Assume ERC20 for now
				}
				if tokenId, ok := params["tokenId"].(string); ok {
					transfer.TokenID = tokenId
					transfer.Type = "ERC721" // NFT transfer
				}
			}

			// Token metadata will be added later by the metadata enricher

			transfers = append(transfers, transfer)
		}
	}

	return transfers
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

// formatAmount formats a raw amount using decimals (simplified version)
func formatAmount(amount string, decimals int) string {
	if amount == "" {
		return "0"
	}
	
	// Handle hex strings
	if strings.HasPrefix(amount, "0x") {
		// Convert hex to decimal
		amountBig := new(big.Int)
		amountBig.SetString(amount[2:], 16)
		amount = amountBig.String()
	}

	// Parse the amount
	amountBig := new(big.Int)
	amountBig, ok := amountBig.SetString(amount, 10)
	if !ok {
		return "0"
	}

	// Convert to float and adjust for decimals
	if decimals > 0 {
		// Convert to big.Float for proper decimal division
		amountFloat := new(big.Float).SetInt(amountBig)
		divisor := new(big.Float).SetFloat64(math.Pow10(decimals))
		result := new(big.Float).Quo(amountFloat, divisor)
		
		// Format with reasonable precision
		formatted := result.Text('f', 6) // 6 decimal places
		// Remove trailing zeros
		formatted = strings.TrimRight(formatted, "0")
		formatted = strings.TrimRight(formatted, ".")
		
		return formatted
	} else {
		// No decimals, just show the integer value
		return amountBig.String()
	}
} 