package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tmc/langchaingo/llms"
	"github.com/txplain/txplain/internal/models"
)

// MonetaryValueEnricher identifies and enriches all monetary values with USD equivalents
type MonetaryValueEnricher struct {
	llm        llms.Model
	apiKey     string
	httpClient *http.Client
}

// EnrichedAmount represents a detected amount with USD value calculations
type EnrichedAmount struct {
	DetectedAmount          // Embed the original detected amount
	FormattedAmount string  `json:"formatted_amount"` // Human-readable amount (e.g., "1.234567")
	USDValue        float64 `json:"usd_value"`        // USD equivalent value
	USDFormatted    string  `json:"usd_formatted"`    // Formatted USD value (e.g., "1,234.56")
	TokenDecimals   int     `json:"token_decimals"`   // Token decimals used for conversion
	PricePerToken   float64 `json:"price_per_token"`  // USD price per token used
	PriceSource     string  `json:"price_source"`     // Source of price data ("CEX", "DEX", "COMBINED")
	HasPriceData    bool    `json:"has_price_data"`   // Whether USD calculation was possible
}

// NewMonetaryValueEnricher creates a new monetary value enricher
func NewMonetaryValueEnricher(llm llms.Model, coinMarketCapAPIKey string) *MonetaryValueEnricher {
	return &MonetaryValueEnricher{
		llm:        llm,
		apiKey:     coinMarketCapAPIKey,
		httpClient: &http.Client{Timeout: 60 * time.Second}, // Increased for price lookups
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
	return []string{"amounts_finder", "erc20_price_lookup"}
}

// Process enriches detected amounts with USD equivalents
func (m *MonetaryValueEnricher) Process(ctx context.Context, baggage map[string]interface{}) error {
	// Get detected amounts from amounts_finder
	detectedAmounts, ok := baggage["detected_amounts"].([]DetectedAmount)
	if !ok || len(detectedAmounts) == 0 {
		return nil // No amounts detected, nothing to enrich
	}

	// Get token prices from erc20_price_lookup
	tokenPrices, hasPrices := baggage["token_prices"].(map[string]*TokenPrice)

	// Get token metadata for decimal conversion
	tokenMetadata, hasMetadata := baggage["token_metadata"].(map[string]*TokenMetadata)

	if !hasMetadata {
		return nil // Can't convert amounts without metadata
	}

	// Get native token price for gas fee calculations
	nativeTokenPrice := m.getNativeTokenPrice(ctx, baggage)

	// Enrich each detected amount with USD value
	enrichedAmounts := make([]EnrichedAmount, 0, len(detectedAmounts))

	for _, amount := range detectedAmounts {
		enriched := m.enrichDetectedAmount(amount, tokenPrices, tokenMetadata, hasPrices)
		if enriched != nil {
			enrichedAmounts = append(enrichedAmounts, *enriched)
		}
	}

	// Add enriched amounts to baggage
	baggage["enriched_amounts"] = enrichedAmounts

	// Enrich raw data with gas fees (unchanged - still needed)
	if err := m.enrichRawData(baggage, nativeTokenPrice); err != nil {
		return fmt.Errorf("failed to enrich raw data: %w", err)
	}

	return nil
}

// enrichDetectedAmount converts a detected amount to an enriched amount with USD values
func (m *MonetaryValueEnricher) enrichDetectedAmount(detected DetectedAmount, tokenPrices map[string]*TokenPrice, tokenMetadata map[string]*TokenMetadata, hasPrices bool) *EnrichedAmount {
	// Get token metadata for decimal conversion
	metadata, exists := tokenMetadata[strings.ToLower(detected.TokenContract)]
	if !exists {
		return nil // Can't convert without decimals
	}

	// Convert raw amount to formatted amount
	formattedAmount := m.convertAmountToTokens(detected.Amount, metadata.Decimals)
	if formattedAmount == 0 {
		return nil // Skip zero amounts
	}

	// Format the amount for display
	formattedStr := m.formatAmount(formattedAmount)

	// Create enriched amount with basic info
	enriched := &EnrichedAmount{
		DetectedAmount:  detected,
		FormattedAmount: formattedStr,
		TokenDecimals:   metadata.Decimals,
		HasPriceData:    false,
	}

	// Add USD value if price data is available
	if hasPrices {
		if price, exists := tokenPrices[strings.ToLower(detected.TokenContract)]; exists && price.Price > 0 {
			usdValue := formattedAmount * price.Price
			enriched.USDValue = usdValue
			enriched.USDFormatted = fmt.Sprintf("%.2f", usdValue)
			enriched.PricePerToken = price.Price
			enriched.PriceSource = price.PriceSource
			enriched.HasPriceData = true
		}
	}

	return enriched
}

// formatAmount formats a token amount for display
func (m *MonetaryValueEnricher) formatAmount(amount float64) string {
	if amount >= 1000000 {
		// For millions and above, use 2 decimal places
		return fmt.Sprintf("%.2f", amount)
	} else if amount >= 1000 {
		// For thousands, use 3 decimal places
		return fmt.Sprintf("%.3f", amount)
	} else if amount >= 1 {
		// For regular amounts, use 6 decimal places
		return fmt.Sprintf("%.6f", amount)
	} else {
		// For very small amounts, use more precision
		return fmt.Sprintf("%.8f", amount)
	}
}

// isAmountParameter checks if a parameter name likely contains an amount/value
func (m *MonetaryValueEnricher) isAmountParameter(paramName string) bool {
	// Common parameter names that contain amounts/values
	amountParams := map[string]bool{
		"value":    true, // ERC20/ERC721 transfers
		"amount":   true, // Generic amounts
		"quantity": true, // NFT quantities
		"balance":  true, // Balance changes
		"supply":   true, // Token supply changes
		"deposit":  true, // Deposit amounts
		"withdraw": true, // Withdrawal amounts
		"payment":  true, // Payment amounts
		"fee":      true, // Fee amounts
		"reward":   true, // Reward amounts
		"stake":    true, // Staking amounts
		"loan":     true, // Loan amounts
		"debt":     true, // Debt amounts
		"price":    true, // Price values
		"cost":     true, // Cost values
	}

	// Check exact match first
	lowerParam := strings.ToLower(paramName)
	if amountParams[lowerParam] {
		return true
	}

	// Check if parameter name contains amount-related keywords
	for keyword := range amountParams {
		if strings.Contains(lowerParam, keyword) {
			return true
		}
	}

	return false
}

// enrichTransfers enriches transfer objects with formatted amounts and USD values
func (m *MonetaryValueEnricher) enrichTransfers(baggage map[string]interface{}, tokenMetadata map[string]*TokenMetadata, tokenPrices map[string]*TokenPrice) error {
	transfers, ok := baggage["transfers"].([]models.TokenTransfer)
	if !ok {
		return nil
	}

	// Get network ID for native token detection
	networkID := int64(1) // Default fallback
	if rawData, ok := baggage["raw_data"].(map[string]interface{}); ok {
		if nid, ok := rawData["network_id"].(float64); ok {
			networkID = int64(nid)
		}
	}

	var enrichmentDebug []string
	for i, transfer := range transfers {
		transferDebug := fmt.Sprintf("Transfer %d (contract: %s)", i, transfer.Contract)

		// Check if this is a native token transfer using pattern detection
		isNativeToken := m.isNativeTokenAddress(transfer.Contract)
		if isNativeToken {
			// This is a native token transfer - enrich with native token info
			nativeSymbol := m.getNativeTokenSymbol(networkID)
			if nativeSymbol != "" {
				transfers[i].Symbol = nativeSymbol
				transfers[i].Name = fmt.Sprintf("%s Native Token", nativeSymbol)
				transfers[i].Decimals = 18   // Native tokens typically use 18 decimals
				transfers[i].Type = "NATIVE" // Generic type for native tokens
				transferDebug += fmt.Sprintf(" - NATIVE TOKEN: %s", nativeSymbol)
			} else {
				// Fallback when native token symbol is unknown
				transfers[i].Symbol = "NATIVE"
				transfers[i].Name = "Native Token"
				transfers[i].Decimals = 18
				transfers[i].Type = "NATIVE"
				transferDebug += " - NATIVE TOKEN: Unknown Symbol"
			}
		}

		// Get token metadata (skip if already set for native tokens)
		var metadata *TokenMetadata
		if !isNativeToken {
			metadata = tokenMetadata[strings.ToLower(transfer.Contract)]
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

			// Set metadata fields for ERC20 tokens
			transfers[i].Name = metadata.Name
			transfers[i].Symbol = metadata.Symbol
			transfers[i].Decimals = metadata.Decimals
		}

		// Always try to format amount if we have metadata, even without price
		if transfer.Amount != "" {
			formattedAmount := m.convertAmountToTokens(transfer.Amount, transfers[i].Decimals)
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

	// Only store debug information in DEBUG mode to avoid overwhelming baggage
	if os.Getenv("DEBUG") == "true" && len(enrichmentDebug) > 0 {
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
		if event.Parameters != nil {
			// Get token metadata for this contract
			metadata := tokenMetadata[strings.ToLower(event.Contract)]
			if metadata == nil {
				continue
			}

			// Look for ANY parameter that could represent a value/amount
			for paramName, paramValue := range event.Parameters {
				if valueStr, ok := paramValue.(string); ok && valueStr != "" {
					// Check if this parameter name suggests it contains an amount
					if m.isAmountParameter(paramName) {
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
						events[i].Parameters[paramName+"_formatted"] = formattedStr

						// Calculate USD value only if price data is available
						price := tokenPrices[strings.ToLower(event.Contract)]
						if price != nil {
							usdValue := formattedAmount * price.Price
							events[i].Parameters[paramName+"_usd"] = fmt.Sprintf("%.2f", usdValue)
						}
					}
				}
			}
		}
	}

	// Update events in baggage
	baggage["events"] = events
	return nil
}

// getNativeTokenPrice gets the USD price for the native token of the network
func (m *MonetaryValueEnricher) getNativeTokenPrice(ctx context.Context, baggage map[string]interface{}) float64 {
	// Get network ID from baggage
	rawData, ok := baggage["raw_data"].(map[string]interface{})
	if !ok {
		return 0 // No fallback price - let system work without prices
	}

	networkID, ok := rawData["network_id"].(float64)
	if !ok {
		return 0 // No fallback price - let system work without prices
	}

	// Map network ID to native token symbol
	nativeTokenSymbol := m.getNativeTokenSymbol(int64(networkID))
	if nativeTokenSymbol == "" || m.apiKey == "" {
		// No native token symbol or API key - return 0
		return 0
	}

	// Fetch actual price from CoinMarketCap API
	price, err := m.fetchNativeTokenPrice(ctx, nativeTokenSymbol)
	if err != nil {
		// If price fetch fails, return 0 instead of hardcoded fallback
		return 0
	}

	return price
}

// getNativeTokenSymbol returns the native token symbol for a given network
// Uses network configuration and pattern detection instead of hardcoding chain IDs
func (m *MonetaryValueEnricher) getNativeTokenSymbol(networkID int64) string {
	return m.getNativeTokenSymbolFromNetwork(networkID)
}

// getNativeTokenFromRPC attempts to get native token symbol from RPC or network context
// This is a completely generic approach that works with any network
func (m *MonetaryValueEnricher) getNativeTokenFromRPC(network models.Network) string {
	// Check if network has native token info configured
	// This could be extended to use RPC calls or network metadata
	// For now, return empty to let system work without hardcoded assumptions
	return ""
}

// fetchNativeTokenPrice fetches the current USD price from CoinMarketCap API
func (m *MonetaryValueEnricher) fetchNativeTokenPrice(ctx context.Context, symbol string) (float64, error) {
	// Build query parameters
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("convert", "USD")

	// Construct URL
	quoteURL := fmt.Sprintf("https://pro-api.coinmarketcap.com/v1/cryptocurrency/quotes/latest?%s", params.Encode())

	// Make request
	req, err := http.NewRequestWithContext(ctx, "GET", quoteURL, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-CMC_PRO_API_KEY", m.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response: %w", err)
	}

	// Define response structure
	var response struct {
		Status struct {
			ErrorCode    int    `json:"error_code"`
			ErrorMessage string `json:"error_message"`
		} `json:"status"`
		Data map[string]struct {
			Quote map[string]struct {
				Price float64 `json:"price"`
			} `json:"quote"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &response); err != nil {
		return 0, fmt.Errorf("failed to parse response: %w", err)
	}

	if response.Status.ErrorCode != 0 {
		return 0, fmt.Errorf("API error: %s", response.Status.ErrorMessage)
	}

	// Extract price data
	tokenData, exists := response.Data[symbol]
	if !exists {
		return 0, fmt.Errorf("token data not found for symbol %s", symbol)
	}

	quoteData, exists := tokenData.Quote["USD"]
	if !exists {
		return 0, fmt.Errorf("USD quote not found for symbol %s", symbol)
	}

	return quoteData.Price, nil
}

// getFallbackNativeTokenPrice returns generic fallback price
// Uses RPC-first approach, then API fallbacks, no hardcoded prices
func (m *MonetaryValueEnricher) getFallbackNativeTokenPrice(networkID int64) float64 {
	// Generic approach: try to fetch current price via API for any network
	nativeSymbol := m.getNativeTokenSymbol(networkID)
	if nativeSymbol != "" && m.apiKey != "" {
		// Try to fetch actual current price from API
		if price, err := m.fetchNativeTokenPrice(context.Background(), nativeSymbol); err == nil {
			return price
		}
	}

	// No hardcoded prices - return 0 to indicate price unavailable
	// This is better than hardcoding outdated prices
	// The system will work without USD values but still show token amounts
	return 0
}

// getNativeTokenPriceFromAPI gets native token price using CoinMarketCap API
// This is a completely generic approach that works for any network
func (m *MonetaryValueEnricher) getNativeTokenPriceFromAPI(networkID int64) float64 {
	// Get native token symbol for this network
	nativeSymbol := m.getNativeTokenSymbol(networkID)
	if nativeSymbol == "" {
		return 0
	}

	// Use existing CoinMarketCap API to get current price
	if m.apiKey != "" {
		if price, err := m.fetchNativeTokenPrice(context.Background(), nativeSymbol); err == nil {
			return price
		}
	}

	// Fallback: use the existing fallback mechanism
	return m.getFallbackNativeTokenPrice(networkID)
}

// enrichRawData enriches raw transaction data with USD values
func (m *MonetaryValueEnricher) enrichRawData(baggage map[string]interface{}, nativeTokenPriceUSD float64) error {
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
						// Calculate gas fee in native token and USD
						gasFeeWei := gasUsed * effectiveGasPrice
						gasFeeNative := float64(gasFeeWei) / math.Pow10(18)
						gasFeeUSD := gasFeeNative * nativeTokenPriceUSD

						receipt["gas_fee_native"] = fmt.Sprintf("%.6f", gasFeeNative)
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

	// Use generic approach: rely on RPC data and amount pattern analysis
	// instead of hardcoding assumptions about specific token symbols
	// This approach works for any token without symbol-based assumptions

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

// GetPromptContext provides monetary context for LLM prompts
func (m *MonetaryValueEnricher) GetPromptContext(ctx context.Context, baggage map[string]interface{}) string {
	// Get enriched amounts from baggage
	enrichedAmounts, ok := baggage["enriched_amounts"].([]EnrichedAmount)
	if !ok || len(enrichedAmounts) == 0 {
		// Still provide gas fee context if available
		return m.getGasFeeContext(baggage)
	}

	var contextParts []string
	contextParts = append(contextParts, "### ENRICHED AMOUNTS:")

	// Group amounts by type for better organization
	amountsByType := make(map[string][]EnrichedAmount)
	for _, amount := range enrichedAmounts {
		amountsByType[amount.AmountType] = append(amountsByType[amount.AmountType], amount)
	}

	// Display amounts grouped by type
	for amountType, amounts := range amountsByType {
		typeHeader := fmt.Sprintf("\n%s AMOUNTS:", strings.ToUpper(amountType))
		contextParts = append(contextParts, typeHeader)

		for i, amount := range amounts {
			amountInfo := fmt.Sprintf("\nAmount #%d:", i+1)

			// Token and amount info
			if amount.TokenSymbol != "" {
				amountInfo += fmt.Sprintf("\n- Token: %s (%s)", amount.TokenSymbol, amount.TokenContract)
			} else {
				amountInfo += fmt.Sprintf("\n- Token: %s", amount.TokenContract)
			}

			amountInfo += fmt.Sprintf("\n- Amount: %s", amount.FormattedAmount)

			// USD value if available
			if amount.HasPriceData {
				amountInfo += fmt.Sprintf("\n- USD Value: $%s", amount.USDFormatted)
				amountInfo += fmt.Sprintf("\n- Price per Token: $%.6f", amount.PricePerToken)
				if amount.PriceSource != "" {
					amountInfo += fmt.Sprintf(" [%s]", amount.PriceSource)
				}
			} else {
				amountInfo += "\n- USD Value: Not available (no price data)"
			}

			// Flow information
			if amount.FromAddress != "" && amount.ToAddress != "" {
				amountInfo += fmt.Sprintf("\n- Flow: %s → %s", amount.FromAddress, amount.ToAddress)
			}

			// Context and confidence
			amountInfo += fmt.Sprintf("\n- Context: %s", amount.Context)
			amountInfo += fmt.Sprintf("\n- Confidence: %.1f%%", amount.Confidence*100)

			contextParts = append(contextParts, amountInfo)
		}
	}

	// Add gas fee context
	gasFeeContext := m.getGasFeeContext(baggage)
	if gasFeeContext != "" {
		contextParts = append(contextParts, "\n"+gasFeeContext)
	}

	return strings.Join(contextParts, "\n")
}

// getGasFeeContext provides gas fee context regardless of other amounts
func (m *MonetaryValueEnricher) getGasFeeContext(baggage map[string]interface{}) string {
	rawData, ok := baggage["raw_data"].(map[string]interface{})
	if !ok {
		return ""
	}

	receipt, ok := rawData["receipt"].(map[string]interface{})
	if !ok {
		return ""
	}

	// Check if gas fee USD is already calculated
	if gasFeeUSD, ok := receipt["gas_fee_usd"].(string); ok && gasFeeUSD != "" {
		if gasFeeNative, ok := receipt["gas_fee_native"].(string); ok && gasFeeNative != "" {
			return fmt.Sprintf("### TRANSACTION FEES:\n- Gas Fee: %s native tokens = $%s USD", gasFeeNative, gasFeeUSD)
		}
		return fmt.Sprintf("### TRANSACTION FEES:\n- Gas Fee: $%s USD", gasFeeUSD)
	}

	return ""
}

// NetFlowData represents net flow information for an address
type NetFlowData struct {
	NetAmount       float64
	FormattedAmount string
	TransferCount   int
}

// calculateNetFlows calculates net token flows per address to identify main transaction effects
func (m *MonetaryValueEnricher) calculateNetFlows(transfers []models.TokenTransfer) map[string]map[string]*NetFlowData {
	// Map: token_symbol -> address -> net_flow_data
	flows := make(map[string]map[string]*NetFlowData)

	for _, transfer := range transfers {
		token := transfer.Symbol
		if token == "" {
			token = transfer.Contract
		}

		if flows[token] == nil {
			flows[token] = make(map[string]*NetFlowData)
		}

		// Parse amount
		var amount float64
		if transfer.FormattedAmount != "" {
			if amt, err := strconv.ParseFloat(transfer.FormattedAmount, 64); err == nil {
				amount = amt
			}
		}

		// Update sender (negative)
		if transfer.From != "" {
			if flows[token][transfer.From] == nil {
				flows[token][transfer.From] = &NetFlowData{}
			}
			flows[token][transfer.From].NetAmount -= amount
			flows[token][transfer.From].TransferCount++
		}

		// Update receiver (positive)
		if transfer.To != "" {
			if flows[token][transfer.To] == nil {
				flows[token][transfer.To] = &NetFlowData{FormattedAmount: transfer.FormattedAmount}
			}
			flows[token][transfer.To].NetAmount += amount
			flows[token][transfer.To].TransferCount++

			// Store formatted amount for the largest positive flow
			if flows[token][transfer.To].NetAmount > 0 && transfer.FormattedAmount != "" {
				flows[token][transfer.To].FormattedAmount = fmt.Sprintf("%.6f", flows[token][transfer.To].NetAmount)
			}
		}
	}

	return flows
}

// looksLikePurchase determines if the transaction pattern suggests a purchase
func (m *MonetaryValueEnricher) looksLikePurchase(transfers []models.TokenTransfer, netFlows map[string]map[string]*NetFlowData) bool {
	// Check if there are NFT transfers in the baggage (would be set by NFT decoder)
	// This is a strong indicator of a purchase
	// We can't directly access baggage here, but the presence of small net flows with
	// multiple intermediary transfers suggests a purchase with fees/routing

	// Look for pattern: one address sends significant amount, multiple addresses receive smaller amounts
	for _, addressFlows := range netFlows {
		var senders, receivers int
		var maxSent, totalReceived float64

		for _, flow := range addressFlows {
			if flow.NetAmount < 0 {
				senders++
				if -flow.NetAmount > maxSent {
					maxSent = -flow.NetAmount
				}
			} else if flow.NetAmount > 0 {
				receivers++
				totalReceived += flow.NetAmount
			}
		}

		// Pattern: one main sender, multiple receivers with small amounts suggests purchase/fees
		if senders == 1 && receivers > 2 && maxSent > totalReceived*1.5 {
			return true
		}
	}

	return false
}

// looksLikeSwap determines if the transaction pattern suggests a token swap
func (m *MonetaryValueEnricher) looksLikeSwap(transfers []models.TokenTransfer, netFlows map[string]map[string]*NetFlowData) bool {
	// Look for pattern: user sends one token type and receives a different token type
	// with minimal intermediary transfers

	if len(netFlows) == 2 { // Exactly two different tokens
		var userAddress string
		var sentToken, receivedToken string

		for token, addressFlows := range netFlows {
			for address, flow := range addressFlows {
				if flow.NetAmount < 0 && userAddress == "" {
					userAddress = address
					sentToken = token
				} else if flow.NetAmount > 0 && address == userAddress {
					receivedToken = token
				}
			}
		}

		return userAddress != "" && sentToken != "" && receivedToken != "" && sentToken != receivedToken
	}

	return false
}

// getGasFeeInUSD extracts gas fee in USD if available
func (m *MonetaryValueEnricher) getGasFeeInUSD(baggage map[string]interface{}) string {
	// Use the enhanced calculateGasFeeUSD method
	return m.calculateGasFeeUSD(baggage)
}

// PaymentFlowData represents payment flow analysis
type PaymentFlowData struct {
	Payer            string
	FinalRecipient   string
	ActualUser       string // The actual user/beneficiary (from protocol events)
	UserRole         string // "borrower", "lender", "trader", etc.
	Token            string
	InitialAmount    string
	InitialAmountUSD string
	FinalAmount      string
	FinalAmountUSD   string
	TotalFees        string
	TotalFeesUSD     string
	FeeRecipients    []FeeRecipient
	GasFeeUSD        string
	NetworkFees      string
	NetworkFeesUSD   string
}

// FeeRecipient represents an address that received fees
type FeeRecipient struct {
	Address   string
	Amount    string
	AmountUSD string
}

// analyzePaymentFlow analyzes the payment flow to identify payer, recipient, and fees
func (m *MonetaryValueEnricher) analyzePaymentFlow(transfers []models.TokenTransfer, baggage map[string]interface{}) *PaymentFlowData {
	if len(transfers) == 0 {
		return nil
	}

	// Find the largest initial transfer (likely the main payment)
	var largestTransfer *models.TokenTransfer
	var largestAmount float64

	for i, transfer := range transfers {
		if transfer.FormattedAmount != "" {
			if amount, err := strconv.ParseFloat(transfer.FormattedAmount, 64); err == nil {
				if amount > largestAmount {
					largestAmount = amount
					largestTransfer = &transfers[i]
				}
			}
		}
	}

	if largestTransfer == nil {
		return nil
	}

	// Identify the actual user from protocol events
	actualUser, userRole := m.identifyActualUserFromEvents(baggage)

	paymentFlow := &PaymentFlowData{
		Payer:            largestTransfer.From,
		Token:            largestTransfer.Symbol,
		InitialAmount:    largestTransfer.FormattedAmount,
		InitialAmountUSD: largestTransfer.AmountUSD,
		ActualUser:       actualUser,
		UserRole:         userRole,
		FeeRecipients:    []FeeRecipient{},
	}

	// Find the final recipient (largest outgoing transfer to a user address that's not the payer)
	// and identify fee recipients
	var finalTransfer *models.TokenTransfer
	var finalAmount float64
	var totalFeesAmount float64

	for i, transfer := range transfers {
		if transfer.FormattedAmount != "" && transfer.Symbol == paymentFlow.Token {
			if amount, err := strconv.ParseFloat(transfer.FormattedAmount, 64); err == nil {
				// Skip transfers from the payer (these are outgoing payments)
				if transfer.From == paymentFlow.Payer {
					continue
				}

				// Check if this might be a final recipient transfer (larger amount)
				if amount > finalAmount {
					// If we had a previous final transfer, it's now a fee recipient
					if finalTransfer != nil {
						paymentFlow.FeeRecipients = append(paymentFlow.FeeRecipients, FeeRecipient{
							Address:   finalTransfer.To,
							Amount:    finalTransfer.FormattedAmount,
							AmountUSD: finalTransfer.AmountUSD,
						})
						totalFeesAmount += finalAmount
					}

					finalAmount = amount
					finalTransfer = &transfers[i]
				} else {
					// This is likely a fee recipient (smaller amount)
					paymentFlow.FeeRecipients = append(paymentFlow.FeeRecipients, FeeRecipient{
						Address:   transfer.To,
						Amount:    transfer.FormattedAmount,
						AmountUSD: transfer.AmountUSD,
					})
					totalFeesAmount += amount
				}
			}
		}
	}

	if finalTransfer != nil {
		paymentFlow.FinalRecipient = finalTransfer.To
		paymentFlow.FinalAmount = finalTransfer.FormattedAmount
		paymentFlow.FinalAmountUSD = finalTransfer.AmountUSD

		// Calculate total fees based on tracked fee recipients
		if totalFeesAmount > 0 {
			paymentFlow.TotalFees = fmt.Sprintf("%.3f", totalFeesAmount)

			// Calculate total USD fees from fee recipients
			var totalFeesUSD float64
			for _, feeRecipient := range paymentFlow.FeeRecipients {
				if feeRecipient.AmountUSD != "" {
					if feeUSD, err := strconv.ParseFloat(feeRecipient.AmountUSD, 64); err == nil {
						totalFeesUSD += feeUSD
					}
				}
			}
			if totalFeesUSD > 0 {
				paymentFlow.TotalFeesUSD = fmt.Sprintf("%.2f", totalFeesUSD)
			}
		}
	}

	// Calculate gas fees from transaction data
	paymentFlow.GasFeeUSD = m.calculateGasFeeUSD(baggage)

	return paymentFlow
}

// calculateGasFeeUSD calculates gas fee in USD with proper native token pricing
func (m *MonetaryValueEnricher) calculateGasFeeUSD(baggage map[string]interface{}) string {
	rawData, ok := baggage["raw_data"].(map[string]interface{})
	if !ok {
		return ""
	}

	receipt, ok := rawData["receipt"].(map[string]interface{})
	if !ok {
		return ""
	}

	// First check if gas_fee_usd is already calculated (from enrichRawData)
	if gasFeeUSD, ok := receipt["gas_fee_usd"].(string); ok && gasFeeUSD != "" {
		return gasFeeUSD
	}

	gasUsedHex, hasGasUsed := receipt["gasUsed"].(string)
	effectiveGasPriceHex, hasGasPrice := receipt["effectiveGasPrice"].(string)

	if !hasGasUsed || !hasGasPrice {
		return ""
	}

	// Parse gas values
	gasUsed, err1 := strconv.ParseUint(gasUsedHex[2:], 16, 64)
	gasPrice, err2 := strconv.ParseUint(effectiveGasPriceHex[2:], 16, 64)

	if err1 != nil || err2 != nil {
		return ""
	}

	// Calculate fee in native token (wei for Ethereum-based chains)
	feeWei := gasUsed * gasPrice
	feeEth := float64(feeWei) / 1e18

	// Get network-specific native token price
	networkID, ok := rawData["network_id"].(float64)
	if !ok {
		return ""
	}

	// Generic native token pricing using CoinMarketCap API
	nativeTokenPrice := m.getNativeTokenPriceFromAPI(int64(networkID))
	if nativeTokenPrice == 0 {
		// If we can't get price, still show the ETH amount
		if feeEth > 0.0001 {
			return fmt.Sprintf("%.6f %s", feeEth, m.getNativeTokenSymbol(int64(networkID)))
		}
		return ""
	}

	feeUSD := feeEth * nativeTokenPrice
	if feeUSD > 0.001 { // Only show if meaningful amount
		return fmt.Sprintf("%.3f", feeUSD)
	}

	return "0.001" // Show minimal fee for very small amounts
}

// isNFTMinting checks if this transaction involves NFT minting
func (m *MonetaryValueEnricher) isNFTMinting(baggage map[string]interface{}) bool {
	// Check for NFT transfers from zero address
	if nftTransfers, ok := baggage["nft_transfers"]; ok {
		// Handle different possible types for NFT transfers
		switch transfers := nftTransfers.(type) {
		case []map[string]interface{}:
			for _, transfer := range transfers {
				if from, ok := transfer["from"].(string); ok {
					if from == "0x0000000000000000000000000000000000000000" {
						return true
					}
				}
			}
		default:
			// Check all events generically for zero address transfers (minting pattern)
			if events, ok := baggage["events"].([]models.Event); ok {
				for _, event := range events {
					if event.Parameters != nil {
						// Look for any event with a "from" parameter set to zero address
						if from, ok := event.Parameters["from"].(string); ok {
							if from == "0x0000000000000000000000000000000000000000" {
								return true
							}
						}
					}
				}
			}
		}
	}

	return false
}

// getNFTRecipients gets the list of NFT recipients
func (m *MonetaryValueEnricher) getNFTRecipients(baggage map[string]interface{}) []string {
	var recipients []string

	// Check all events generically for zero address transfers (minting pattern)
	if events, ok := baggage["events"].([]models.Event); ok {
		for _, event := range events {
			if event.Parameters != nil {
				// Look for any event with "from" zero address and "to" recipient
				if from, ok := event.Parameters["from"].(string); ok {
					if from == "0x0000000000000000000000000000000000000000" {
						if to, ok := event.Parameters["to"].(string); ok {
							recipients = append(recipients, to)
						}
					}
				}
			}
		}
	}

	return m.uniqueRecipients(recipients)
}

// uniqueRecipients returns unique recipients
func (m *MonetaryValueEnricher) uniqueRecipients(recipients []string) []string {
	seen := make(map[string]bool)
	var unique []string

	for _, recipient := range recipients {
		if !seen[recipient] {
			seen[recipient] = true
			unique = append(unique, recipient)
		}
	}

	return unique
}

// identifyActualUserFromEvents analyzes protocol events to find the actual user/beneficiary
func (m *MonetaryValueEnricher) identifyActualUserFromEvents(baggage map[string]interface{}) (string, string) {
	rawData, ok := baggage["raw_data"].(map[string]interface{})
	if !ok {
		return "", ""
	}

	logs, ok := rawData["logs"].([]interface{})
	if !ok {
		return "", ""
	}

	// Check events for user identification patterns
	for _, logInterface := range logs {
		logMap, ok := logInterface.(map[string]interface{})
		if !ok {
			continue
		}

		// Look for decoded event data
		if eventsInterface, ok := baggage["events"]; ok {
			if events, ok := eventsInterface.([]models.Event); ok {
				for _, event := range events {
					user, role := m.extractUserFromEvent(event)
					if user != "" {
						return user, role
					}
				}
			}
		}

		// Also check raw event parameters for common DeFi user patterns
		if address, role := m.extractUserFromEventParameters(logMap); address != "" {
			return address, role
		}
	}

	return "", ""
}

// extractUserFromEvent extracts user information from decoded events
func (m *MonetaryValueEnricher) extractUserFromEvent(event models.Event) (string, string) {
	params := event.Parameters

	// Generic DeFi user identification patterns - work with ANY event type
	if onBehalf, ok := params["onBehalf"].(string); ok && onBehalf != "" {
		// onBehalf parameter is common in lending protocols (Aave, Morpho, etc.)
		// Let the LLM determine the role based on context rather than hardcoded event names
		return onBehalf, "user"
	}

	if borrower, ok := params["borrower"].(string); ok && borrower != "" {
		return borrower, "borrower"
	}

	if lender, ok := params["lender"].(string); ok && lender != "" {
		return lender, "lender"
	}

	if user, ok := params["user"].(string); ok && user != "" {
		return user, "user"
	}

	if account, ok := params["account"].(string); ok && account != "" {
		return account, "user"
	}

	if owner, ok := params["owner"].(string); ok && owner != "" {
		// Common in token approvals, NFT transfers
		return owner, "owner"
	}

	if recipient, ok := params["recipient"].(string); ok && recipient != "" {
		return recipient, "recipient"
	}

	return "", ""
}

// extractUserFromEventParameters extracts user from raw event parameters
func (m *MonetaryValueEnricher) extractUserFromEventParameters(logMap map[string]interface{}) (string, string) {
	// This is a fallback for when events aren't fully decoded
	// We can look for common patterns in raw data or topics

	topics, ok := logMap["topics"].([]interface{})
	if !ok || len(topics) < 2 {
		return "", ""
	}

	// For indexed parameters in topics, we can try to identify user addresses
	// This is more complex and would require event signature analysis
	// For now, return empty - the main extraction should work from decoded events

	return "", ""
}

// GetAnnotationContext provides context for annotations
func (m *MonetaryValueEnricher) GetAnnotationContext(ctx context.Context, baggage map[string]interface{}) *models.AnnotationContext {
	annotationContext := &models.AnnotationContext{
		Items: make([]models.AnnotationContextItem, 0),
	}

	// Get network information dynamically
	networkID := int64(1) // Default fallback
	if rawData, ok := baggage["raw_data"].(map[string]interface{}); ok {
		if nid, ok := rawData["network_id"].(float64); ok {
			networkID = int64(nid)
		}
	}

	// Get network configuration dynamically
	network, networkExists := models.GetNetwork(networkID)
	if !networkExists {
		return annotationContext // Return empty if network not configured
	}

	nativeSymbol := m.getNativeTokenSymbol(networkID)
	nativePrice := m.getNativeTokenPrice(ctx, baggage)

	if nativeSymbol != "" && nativePrice > 0 {
		priceText := fmt.Sprintf("$%.2f", nativePrice)
		if nativePrice < 0.01 {
			priceText = fmt.Sprintf("$%.6f", nativePrice)
		}

		// Use generic names based on available data
		nativeTokenFullName := nativeSymbol // Default to symbol
		if network.Name != "" {
			// Use network name as context for native token full name
			nativeTokenFullName = fmt.Sprintf("%s Native Token", network.Name)
		}

		// Use network explorer URL dynamically
		explorerURL := network.Explorer
		if explorerURL == "" {
			explorerURL = "https://explorer.com" // Generic fallback
		}

		annotationContext.AddItem(models.AnnotationContextItem{
			Type:        "native_token",
			Value:       nativeSymbol,
			Name:        fmt.Sprintf("%s (%s)", nativeTokenFullName, nativeSymbol),
			Link:        explorerURL, // Use dynamic explorer URL
			Description: fmt.Sprintf("Native %s blockchain token - Current price: %s", network.Name, priceText),
			Metadata: map[string]interface{}{
				"price_usd":  nativePrice,
				"symbol":     nativeSymbol,
				"decimals":   18,
				"type":       "native_token",
				"network_id": networkID,
			},
		})
	}

	// Add enriched transfer amounts context
	transfers, ok := baggage["transfers"].([]models.TokenTransfer)
	if ok {
		for _, transfer := range transfers {
			// Add context for formatted amounts with USD values
			if transfer.FormattedAmount != "" && transfer.Symbol != "" && transfer.AmountUSD != "" {
				amountText := fmt.Sprintf("%s %s", transfer.FormattedAmount, transfer.Symbol)
				usdText := fmt.Sprintf("$%s", transfer.AmountUSD)

				description := fmt.Sprintf("Token transfer: %s worth $%s USD", amountText, transfer.AmountUSD)

				// Create tooltip for token amounts
				tooltipRows := []string{
					fmt.Sprintf("<tr><td><strong>Amount:</strong></td><td>%s %s</td></tr>", transfer.FormattedAmount, transfer.Symbol),
					fmt.Sprintf("<tr><td><strong>USD Value:</strong></td><td>$%s</td></tr>", transfer.AmountUSD),
					fmt.Sprintf("<tr><td><strong>Token:</strong></td><td>%s</td></tr>", transfer.Name),
					fmt.Sprintf("<tr><td><strong>Contract:</strong></td><td>%s...%s</td></tr>",
						transfer.Contract[:6], transfer.Contract[len(transfer.Contract)-4:]),
				}
				tooltip := fmt.Sprintf("<table>%s</table>", strings.Join(tooltipRows, ""))

				// Add context for the token amount
				annotationContext.AddItem(models.AnnotationContextItem{
					Type:        "amount",
					Value:       amountText,
					Name:        fmt.Sprintf("%s Amount", transfer.Symbol),
					Description: description,
					Metadata: map[string]interface{}{
						"formatted_amount": transfer.FormattedAmount,
						"symbol":           transfer.Symbol,
						"usd_value":        transfer.AmountUSD,
						"token_name":       transfer.Name,
						"contract":         transfer.Contract,
						"tooltip":          tooltip,
					},
				})

				// Add context for USD amounts
				annotationContext.AddItem(models.AnnotationContextItem{
					Type:        "amount",
					Value:       usdText,
					Name:        "USD Value",
					Description: fmt.Sprintf("$%s USD equivalent of %s", transfer.AmountUSD, amountText),
					Metadata: map[string]interface{}{
						"usd_value":    transfer.AmountUSD,
						"token_amount": transfer.FormattedAmount,
						"token_symbol": transfer.Symbol,
						"tooltip":      tooltip,
					},
				})
			}
		}
	}

	// Add gas fee annotation context if available
	if gasFeeUSD := m.getGasFeeInUSD(baggage); gasFeeUSD != "" {
		// Get raw transaction data to build detailed gas fee tooltip
		var gasFeeTooltip string
		if rawData, ok := baggage["raw_data"].(map[string]interface{}); ok {
			if receipt, ok := rawData["receipt"].(map[string]interface{}); ok {
				tooltipRows := []string{
					fmt.Sprintf("<tr><td><strong>Gas Fee:</strong></td><td>$%s USD</td></tr>", gasFeeUSD),
				}

				// Add native gas amount if available
				if gasFeeNative, ok := receipt["gas_fee_native"].(string); ok {
					networkID := int64(1) // Default fallback
					if nid, ok := rawData["network_id"].(float64); ok {
						networkID = int64(nid)
					}
					nativeSymbol := m.getNativeTokenSymbol(networkID)
					if nativeSymbol != "" {
						tooltipRows = append(tooltipRows, fmt.Sprintf("<tr><td><strong>Native Amount:</strong></td><td>%s %s</td></tr>", gasFeeNative, nativeSymbol))
					}
				}

				// Add gas used and price if available
				if gasUsed, ok := receipt["gasUsed"].(string); ok {
					if gasUsedInt := m.hexToUint64(gasUsed); gasUsedInt > 0 {
						tooltipRows = append(tooltipRows, fmt.Sprintf("<tr><td><strong>Gas Used:</strong></td><td>%s</td></tr>", formatNumber(gasUsedInt)))
					}
				}

				if effectiveGasPrice, ok := receipt["effectiveGasPrice"].(string); ok {
					if gasPriceInt := m.hexToUint64(effectiveGasPrice); gasPriceInt > 0 {
						gasPriceGwei := float64(gasPriceInt) / 1e9
						tooltipRows = append(tooltipRows, fmt.Sprintf("<tr><td><strong>Gas Price:</strong></td><td>%.2f Gwei</td></tr>", gasPriceGwei))
					}
				}

				gasFeeTooltip = fmt.Sprintf("<table>%s</table>", strings.Join(tooltipRows, ""))
			}
		}

		// Add gas fee annotation context item
		annotationContext.AddItem(models.AnnotationContextItem{
			Type:        "gas_fee",
			Value:       fmt.Sprintf("$%s", gasFeeUSD),
			Name:        "Transaction Gas Fee",
			Description: fmt.Sprintf("Gas fee for this transaction: $%s USD", gasFeeUSD),
			Metadata: map[string]interface{}{
				"gas_fee_usd": gasFeeUSD,
				"tooltip":     gasFeeTooltip,
			},
		})
	}

	return annotationContext
}

// formatNumber formats a number with commas for readability
func formatNumber(n uint64) string {
	str := fmt.Sprintf("%d", n)
	if len(str) <= 3 {
		return str
	}

	// Add commas every 3 digits from the right
	var result []rune
	for i, r := range str {
		if i > 0 && (len(str)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, r)
	}
	return string(result)
}

// isNativeTokenAddress checks if an address represents the native token using common patterns
func (m *MonetaryValueEnricher) isNativeTokenAddress(address string) bool {
	if address == "" {
		return false
	}

	// Normalize to lowercase for comparison
	addr := strings.ToLower(strings.TrimSpace(address))

	// Remove 0x prefix if present
	if strings.HasPrefix(addr, "0x") {
		addr = addr[2:]
	}

	// Common native token representations used across DeFi protocols
	nativeTokenPatterns := []string{
		"0000000000000000000000000000000000000000", // Zero address - most common
		"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", // Used by 1inch, Paraswap, etc.
		"000000000000000000000000000000000000dead", // Dead address variant
		"0000000000000000000000000000000000001010", // Used in some protocols
		"1111111111111111111111111111111111111111", // Some protocols use this pattern
	}

	for _, pattern := range nativeTokenPatterns {
		if addr == pattern {
			return true
		}
	}

	return false
}

// getNativeTokenSymbolFromNetwork gets the native token symbol for a network
// This is generic and works with any network without hardcoded assumptions
func (m *MonetaryValueEnricher) getNativeTokenSymbolFromNetwork(networkID int64) string {
	// Check for environment variable first: NATIVE_TOKEN_SYMBOL_CHAIN_<NETWORK_ID>
	envVar := fmt.Sprintf("NATIVE_TOKEN_SYMBOL_CHAIN_%d", networkID)
	if symbol := os.Getenv(envVar); symbol != "" {
		return symbol
	}

	// Check if the network configuration includes native token info
	// This could be enhanced to use RPC calls to get network-specific token info
	// For example: eth_getBalance uses the native token, and we could query
	// network metadata or well-known contract addresses

	// For now, if no environment variable is set and no network-specific data exists,
	// return empty string - the system will work without native token symbols
	// This allows the system to be completely generic and work with any network
	return ""
}
