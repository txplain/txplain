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

	var enrichmentDebug []string
	for i, transfer := range transfers {
		transferDebug := fmt.Sprintf("Transfer %d (contract: %s)", i, transfer.Contract)
		
		// Get token metadata
		metadata := tokenMetadata[strings.ToLower(transfer.Contract)]
		if metadata == nil {
			transferDebug += " - NO METADATA"
			// Try to create fallback metadata based on transfer patterns
			metadata = m.createFallbackMetadata(transfer.Contract, transfer.Amount, transfer.Symbol)
			if metadata == nil {
				transferDebug += " - FALLBACK FAILED"
				enrichmentDebug = append(enrichmentDebug, transferDebug)
				continue
			} else {
				transferDebug += fmt.Sprintf(" - FALLBACK CREATED (decimals=%d)", metadata.Decimals)
			}
		} else {
			transferDebug += fmt.Sprintf(" - METADATA OK (name=%s, symbol=%s, decimals=%d)", metadata.Name, metadata.Symbol, metadata.Decimals)
		}

		// Always set metadata fields regardless of price availability
		transfers[i].Name = metadata.Name
		transfers[i].Symbol = metadata.Symbol
		transfers[i].Decimals = metadata.Decimals

		// Always try to format amount if we have metadata, even without price
		if transfer.Amount != "" {
			formattedAmount := m.convertAmountToTokens(transfer.Amount, metadata.Decimals)
			transferDebug += fmt.Sprintf(" - RAW AMOUNT: %s", transfer.Amount)
			
			// Format the amount based on its magnitude for better readability
			var formattedStr string
			if formattedAmount >= 1000000 {
				// For millions and above, use 2 decimal places
				formattedStr = fmt.Sprintf("%.2f", formattedAmount)
			} else if formattedAmount >= 1000 {
				// For thousands, use 3 decimal places
				formattedStr = fmt.Sprintf("%.3f", formattedAmount)
			} else if formattedAmount >= 1 {
				// For regular amounts, use 6 decimal places
				formattedStr = fmt.Sprintf("%.6f", formattedAmount)
			} else {
				// For very small amounts, use more precision
				formattedStr = fmt.Sprintf("%.8f", formattedAmount)
			}
			
			// Remove trailing zeros and decimal point if not needed
			formattedStr = strings.TrimRight(formattedStr, "0")
			formattedStr = strings.TrimRight(formattedStr, ".")
			transfers[i].FormattedAmount = formattedStr
			transferDebug += fmt.Sprintf(" - FORMATTED: %s", formattedStr)

			// Only calculate USD value if we have price data
			price := tokenPrices[strings.ToLower(transfer.Contract)]
			if price != nil {
				transferDebug += fmt.Sprintf(" - PRICE OK ($%.6f)", price.Price)
				usdValue := formattedAmount * price.Price
				transfers[i].AmountUSD = fmt.Sprintf("%.2f", usdValue)
				transferDebug += fmt.Sprintf(" - USD: $%s", transfers[i].AmountUSD)
			} else {
				transferDebug += " - NO PRICE - USD VALUE NOT CALCULATED"
			}
		} else {
			transferDebug += " - NO AMOUNT"
		}
		
		enrichmentDebug = append(enrichmentDebug, transferDebug)
	}

	// Store debug information
	if len(enrichmentDebug) > 0 {
		if debugInfo, ok := baggage["debug_info"].(map[string]interface{}); ok {
			debugInfo["transfer_enrichment"] = enrichmentDebug
		} else {
			baggage["debug_info"] = map[string]interface{}{
				"transfer_enrichment": enrichmentDebug,
			}
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
				
				// Format the amount based on its magnitude for better readability
				var formattedStr string
				if formattedAmount >= 1000000 {
					// For millions and above, use 2 decimal places
					formattedStr = fmt.Sprintf("%.2f", formattedAmount)
				} else if formattedAmount >= 1000 {
					// For thousands, use 3 decimal places
					formattedStr = fmt.Sprintf("%.3f", formattedAmount)
				} else if formattedAmount >= 1 {
					// For regular amounts, use 6 decimal places
					formattedStr = fmt.Sprintf("%.6f", formattedAmount)
				} else {
					// For very small amounts, use more precision
					formattedStr = fmt.Sprintf("%.8f", formattedAmount)
				}
				
				// Remove trailing zeros and decimal point if not needed
				formattedStr = strings.TrimRight(formattedStr, "0")
				formattedStr = strings.TrimRight(formattedStr, ".")
				events[i].Parameters["value_formatted"] = formattedStr

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
		if _, ok := amountBig.SetString(amountStr[2:], 16); !ok {
			return 0
		}
		amountStr = amountBig.String()
	}

	// Parse the amount as big.Int
	amountBig := new(big.Int)
	if _, ok := amountBig.SetString(amountStr, 10); !ok {
		return 0
	}

	// Convert to big.Float and adjust for decimals to maintain precision
	amountFloat := new(big.Float).SetInt(amountBig)
	
	if decimals > 0 {
		// Create divisor (10^decimals) as big.Float for precise division
		divisor := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil))
		amountFloat.Quo(amountFloat, divisor)
	}

	// Convert to float64 only at the end
	result, _ := amountFloat.Float64()
	return result
}

// createFallbackMetadata creates basic metadata when not available from other sources
func (m *MonetaryValueEnricher) createFallbackMetadata(contractAddress, amount, symbol string) *TokenMetadata {
	// Start with safe defaults
	decimals := 18 // Default to 18 for most ERC20 tokens
	name := symbol // Use symbol as name if available
	
	// Only use symbol-based inference for well-known, stable tokens to avoid hardcoding
	if symbol != "" {
		switch strings.ToUpper(symbol) {
		case "USDT", "USDC": // Stablecoins commonly use 6 decimals
			decimals = 6
		case "WBTC": // Wrapped Bitcoin uses 8 decimals like Bitcoin
			decimals = 8
		}
		// For all other tokens including newer/unknown ones, use amount pattern analysis
	}
	
	// Try to infer decimals from amount patterns if we have transaction data
	if amount != "" && strings.HasPrefix(amount, "0x") {
		inferredDecimals := m.inferDecimalsFromAmount(amount)
		if inferredDecimals >= 0 && inferredDecimals <= 30 {
			// Only override defaults if the inference seems reasonable
			// For common decimals (0, 6, 8, 9, 12, 18), trust the inference more
			commonDecimals := map[int]bool{0: true, 6: true, 8: true, 9: true, 12: true, 18: true}
			if commonDecimals[inferredDecimals] {
				decimals = inferredDecimals
			} else if inferredDecimals < decimals {
				// If inferred decimals are less than default, it might be more accurate
				decimals = inferredDecimals
			}
		}
	}

	return &TokenMetadata{
		Address:  contractAddress,
		Name:     name,
		Symbol:   symbol,
		Decimals: decimals,
		Type:     "ERC20",
	}
}

// inferDecimalsFromAmount analyzes hex amount patterns to guess decimals
func (m *MonetaryValueEnricher) inferDecimalsFromAmount(amount string) int {
	if !strings.HasPrefix(amount, "0x") {
		return -1 // Invalid
	}
	
	// Look at the length of the hex value to guess decimals
	hexLen := len(amount) - 2 // Remove 0x prefix
	if hexLen <= 8 {
		// Small hex values (<=8 chars = <=32 bits) often indicate few/no decimals
		return 0
	} else if hexLen <= 16 {
		// Medium hex values (<=16 chars = <=64 bits) might be 6-8 decimals
		return 6
	} else if hexLen > 20 {
		// Very large hex values often indicate 18+ decimals
		return 18
	}
	
	// For middle range, be conservative
	return 18
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
