package tools

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/tmc/langchaingo/llms"
	"github.com/txplain/txplain/internal/models"
)

// MonetaryValueEnricher identifies and enriches all monetary values with USD equivalents
type MonetaryValueEnricher struct {
	llm llms.Model
}

// NewMonetaryValueEnricher creates a new monetary value enricher
func NewMonetaryValueEnricher(llm llms.Model) *MonetaryValueEnricher {
	return &MonetaryValueEnricher{
		llm: llm,
	}
}

// Name returns the tool name
func (m *MonetaryValueEnricher) Name() string {
	return "monetary_value_enricher"
}

// Description returns the tool description
func (m *MonetaryValueEnricher) Description() string {
	return "Identifies all monetary values in transaction data and enriches them with USD equivalents"
}

// Dependencies returns the tools this processor depends on
func (m *MonetaryValueEnricher) Dependencies() []string {
	return []string{"token_metadata_enricher", "erc20_price_lookup"}
}

// Process enriches all monetary values with USD equivalents
func (m *MonetaryValueEnricher) Process(ctx context.Context, baggage map[string]interface{}) error {
	// Get token metadata and prices from baggage
	tokenMetadata, hasMetadata := baggage["token_metadata"].(map[string]*TokenMetadata)
	tokenPrices, hasPrices := baggage["token_prices"].(map[string]*TokenPrice)
	
	if !hasMetadata || !hasPrices {
		return nil // Can't enrich without metadata and prices
	}

	// Get ETH price for native value calculations (approximate)
	ethPriceUSD := 2500.0 // TODO: Get actual ETH price from API

	// Enrich transfers with formatted amounts and USD values
	if err := m.enrichTransfers(baggage, tokenMetadata, tokenPrices); err != nil {
		return fmt.Errorf("failed to enrich transfers: %w", err)
	}

	// Enrich events with formatted values
	if err := m.enrichEvents(baggage, tokenMetadata, tokenPrices); err != nil {
		return fmt.Errorf("failed to enrich events: %w", err)
	}

	// Enrich raw data with USD values
	if err := m.enrichRawData(baggage, ethPriceUSD); err != nil {
		return fmt.Errorf("failed to enrich raw data: %w", err)
	}

	return nil
}

// enrichTransfers enriches transfer objects with formatted amounts and USD values
func (m *MonetaryValueEnricher) enrichTransfers(baggage map[string]interface{}, tokenMetadata map[string]*TokenMetadata, tokenPrices map[string]*TokenPrice) error {
	transfers, ok := baggage["transfers"].([]models.TokenTransfer)
	if !ok {
		return nil
	}

	for i, transfer := range transfers {
		// Get token metadata
		metadata := tokenMetadata[strings.ToLower(transfer.Contract)]
		if metadata == nil {
			continue
		}

		// Get token price
		price := tokenPrices[strings.ToLower(transfer.Contract)]
		if price == nil {
			continue
		}

		// Add metadata to transfer
		transfers[i].Name = metadata.Name
		transfers[i].Symbol = metadata.Symbol
		transfers[i].Decimals = metadata.Decimals

		// Convert raw amount to formatted amount
		if transfer.Amount != "" {
			formattedAmount := m.convertAmountToTokens(transfer.Amount, metadata.Decimals)
			transfers[i].FormattedAmount = fmt.Sprintf("%.6f", formattedAmount)
			transfers[i].FormattedAmount = strings.TrimRight(transfers[i].FormattedAmount, "0")
			transfers[i].FormattedAmount = strings.TrimRight(transfers[i].FormattedAmount, ".")

			// Calculate USD value
			usdValue := formattedAmount * price.Price
			transfers[i].AmountUSD = fmt.Sprintf("%.2f", usdValue)
		}
	}

	// Update transfers in baggage
	baggage["transfers"] = transfers
	return nil
}

// enrichEvents enriches event parameters with formatted values
func (m *MonetaryValueEnricher) enrichEvents(baggage map[string]interface{}, tokenMetadata map[string]*TokenMetadata, tokenPrices map[string]*TokenPrice) error {
	events, ok := baggage["events"].([]models.Event)
	if !ok {
		return nil
	}

	for i, event := range events {
		if event.Name == "Transfer" && event.Parameters != nil {
			// Get token metadata
			metadata := tokenMetadata[strings.ToLower(event.Contract)]
			if metadata == nil {
				continue
			}

			// Get token price
			price := tokenPrices[strings.ToLower(event.Contract)]
			if price == nil {
				continue
			}

			// Add formatted value if "value" parameter exists
			if valueStr, ok := event.Parameters["value"].(string); ok && valueStr != "" {
				formattedAmount := m.convertAmountToTokens(valueStr, metadata.Decimals)
				events[i].Parameters["value_formatted"] = fmt.Sprintf("%.6f", formattedAmount)
				
				// Calculate USD value
				usdValue := formattedAmount * price.Price
				events[i].Parameters["value_usd"] = fmt.Sprintf("%.2f", usdValue)
			}
		}
	}

	// Update events in baggage
	baggage["events"] = events
	return nil
}

// enrichRawData enriches raw transaction data with USD values
func (m *MonetaryValueEnricher) enrichRawData(baggage map[string]interface{}, ethPriceUSD float64) error {
	rawData, ok := baggage["raw_data"].(map[string]interface{})
	if !ok {
		return nil
	}

	// Enrich gas-related values
	if receipt, ok := rawData["receipt"].(map[string]interface{}); ok {
		// Gas used in USD
		if gasUsedStr, ok := receipt["gasUsed"].(string); ok {
			if gasUsed := m.hexToUint64(gasUsedStr); gasUsed > 0 {
				// Get effective gas price
				if effectiveGasPriceStr, ok := receipt["effectiveGasPrice"].(string); ok {
					if effectiveGasPrice := m.hexToUint64(effectiveGasPriceStr); effectiveGasPrice > 0 {
						// Calculate gas fee in ETH and USD
						gasFeeWei := gasUsed * effectiveGasPrice
						gasFeeETH := float64(gasFeeWei) / math.Pow10(18)
						gasFeeUSD := gasFeeETH * ethPriceUSD

						receipt["gas_fee_eth"] = fmt.Sprintf("%.6f", gasFeeETH)
						receipt["gas_fee_usd"] = fmt.Sprintf("%.2f", gasFeeUSD)
					}
				}
			}
		}
	}

	return nil
}

// convertAmountToTokens converts a raw amount to token units
func (m *MonetaryValueEnricher) convertAmountToTokens(amountStr string, decimals int) float64 {
	if amountStr == "" {
		return 0
	}

	// Handle hex strings
	if strings.HasPrefix(amountStr, "0x") {
		// Convert hex to decimal
		amountBig := new(big.Int)
		amountBig.SetString(amountStr[2:], 16)
		amountStr = amountBig.String()
	}

	// Parse the amount
	amountBig := new(big.Int)
	amountBig, ok := amountBig.SetString(amountStr, 10)
	if !ok {
		return 0
	}

	// Convert to float and adjust for decimals
	amountFloat, _ := new(big.Float).SetInt(amountBig).Float64()
	return amountFloat / math.Pow10(decimals)
}

// hexToUint64 converts hex string to uint64
func (m *MonetaryValueEnricher) hexToUint64(hexStr string) uint64 {
	if strings.HasPrefix(hexStr, "0x") {
		if val, err := strconv.ParseUint(hexStr[2:], 16, 64); err == nil {
			return val
		}
	}
	return 0
}

// GetPromptContext provides enriched monetary context for LLM prompts
func (m *MonetaryValueEnricher) GetPromptContext(ctx context.Context, baggage map[string]interface{}) string {
	var contextParts []string

	// Add enriched transfer information
	if transfers, ok := baggage["transfers"].([]models.TokenTransfer); ok && len(transfers) > 0 {
		contextParts = append(contextParts, "### Enriched Token Transfers:")
		for i, transfer := range transfers {
			transferInfo := fmt.Sprintf("\nTransfer #%d:", i+1)
			transferInfo += fmt.Sprintf("\n- Token: %s (%s)", transfer.Name, transfer.Symbol)
			transferInfo += fmt.Sprintf("\n- From: %s", transfer.From)
			transferInfo += fmt.Sprintf("\n- To: %s", transfer.To)
			
			if transfer.FormattedAmount != "" {
				transferInfo += fmt.Sprintf("\n- Amount: %s %s", transfer.FormattedAmount, transfer.Symbol)
			}
			if transfer.AmountUSD != "" {
				transferInfo += fmt.Sprintf("\n- USD Value: $%s", transfer.AmountUSD)
			}
			
			contextParts = append(contextParts, transferInfo)
		}
	}

	// Add gas fee information
	if rawData, ok := baggage["raw_data"].(map[string]interface{}); ok {
		if receipt, ok := rawData["receipt"].(map[string]interface{}); ok {
			if gasFeeUSD, ok := receipt["gas_fee_usd"].(string); ok {
				contextParts = append(contextParts, fmt.Sprintf("\n### Transaction Fees:\n- Gas Fee: $%s USD", gasFeeUSD))
			}
		}
	}

	if len(contextParts) == 0 {
		return ""
	}

	return strings.Join(contextParts, "\n")
} 