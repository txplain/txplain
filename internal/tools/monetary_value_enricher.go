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
	llm       llms.Model
	cmcClient *CoinMarketCapClient
	verbose   bool
	cache     Cache // Cache for price lookups
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
func NewMonetaryValueEnricher(llm llms.Model, cmcClient *CoinMarketCapClient, cache Cache, verbose bool) *MonetaryValueEnricher {
	return &MonetaryValueEnricher{
		llm:       llm,
		cmcClient: cmcClient,
		verbose:   verbose,
		cache:     cache,
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
	if m.verbose {
		fmt.Println("\n" + strings.Repeat("💰", 60))
		fmt.Println("🔍 MONETARY VALUE ENRICHER: Starting USD value enrichment")
		fmt.Printf("🔑 CoinMarketCap API Key available: %t\n", m.cmcClient.IsAvailable())
		fmt.Println(strings.Repeat("💰", 60))
	}

	// Get progress tracker from baggage for sub-progress updates
	var progressTracker *models.ProgressTracker
	if tracker, ok := baggage["progress_tracker"].(*models.ProgressTracker); ok {
		progressTracker = tracker
	}

	// Sub-step 1: Checking detected amounts
	if progressTracker != nil {
		progressTracker.UpdateComponent(
			"monetary_value_enricher",
			models.ComponentGroupEnrichment,
			"Calculating USD Values",
			models.ComponentStatusRunning,
			"Checking detected amounts for USD conversion...",
		)
	}

	if m.verbose {
		fmt.Println("🔍 Sub-step 1: Checking detected amounts for USD conversion...")
	}

	// Get detected amounts from amounts_finder
	detectedAmounts, ok := baggage["detected_amounts"].([]DetectedAmount)
	if !ok || len(detectedAmounts) == 0 {
		if m.verbose {
			fmt.Println("⚠️  No detected amounts found in baggage, skipping enrichment")
			fmt.Println(strings.Repeat("💰", 60) + "\n")
		}
		return nil // No amounts detected, nothing to enrich
	}

	if m.verbose {
		fmt.Printf("📊 Processing %d detected amounts for USD conversion\n", len(detectedAmounts))
	}

	// Sub-step 2: Gathering price and metadata
	if progressTracker != nil {
		progressTracker.UpdateComponent(
			"monetary_value_enricher",
			models.ComponentGroupEnrichment,
			"Calculating USD Values",
			models.ComponentStatusRunning,
			"Gathering token prices and metadata...",
		)
	}

	if m.verbose {
		fmt.Println("📊 Sub-step 2: Gathering token prices and metadata...")
	}

	// Get token prices from erc20_price_lookup
	tokenPrices, hasPrices := baggage["token_prices"].(map[string]*TokenPrice)

	// Get token metadata for decimal conversion
	tokenMetadata, hasMetadata := baggage["token_metadata"].(map[string]*TokenMetadata)

	if !hasMetadata {
		if m.verbose {
			fmt.Println("❌ No token metadata found, cannot convert amounts")
			fmt.Println(strings.Repeat("💰", 60) + "\n")
		}
		return nil // Can't convert amounts without metadata
	}

	if m.verbose {
		priceStatus := "❌ No price data"
		if hasPrices {
			priceStatus = fmt.Sprintf("✅ Price data for %d tokens", len(tokenPrices))
		}
		fmt.Printf("💱 %s\n", priceStatus)
		fmt.Printf("🏷️  Token metadata for %d tokens\n", len(tokenMetadata))
	}

	// Sub-step 3: Fetching native token price if needed
	if progressTracker != nil {
		progressTracker.UpdateComponent(
			"monetary_value_enricher",
			models.ComponentGroupEnrichment,
			"Calculating USD Values",
			models.ComponentStatusRunning,
			"Fetching native token price for gas calculations...",
		)
	}

	if m.verbose {
		fmt.Println("⛽ Sub-step 3: Fetching native token price for gas calculations...")
	}

	// Get native token price for gas fee calculations
	nativeTokenPrice := m.getNativeTokenPrice(ctx, baggage)
	if m.verbose && nativeTokenPrice > 0 {
		fmt.Printf("⛽ Native token price: $%.6f\n", nativeTokenPrice)
	}

	// Sub-step 4: Converting amounts to USD
	if progressTracker != nil {
		progressTracker.UpdateComponent(
			"monetary_value_enricher",
			models.ComponentGroupEnrichment,
			"Calculating USD Values",
			models.ComponentStatusRunning,
			"Converting detected amounts to USD values...",
		)
	}

	if m.verbose {
		fmt.Println("💰 Sub-step 4: Converting detected amounts to USD values...")
		fmt.Println("🔄 Enriching amounts with USD values...")
	}

	// Enrich each detected amount with USD value
	enrichedAmounts := make([]EnrichedAmount, 0, len(detectedAmounts))
	successCount := 0

	for i, amount := range detectedAmounts {
		if m.verbose {
			fmt.Printf("   [%d/%d] Processing %s %s...", i+1, len(detectedAmounts), amount.Amount, amount.TokenSymbol)
		}

		enriched := m.enrichDetectedAmount(amount, tokenPrices, tokenMetadata, hasPrices, baggage)
		if enriched != nil {
			enrichedAmounts = append(enrichedAmounts, *enriched)
			successCount++

			if m.verbose {
				if enriched.HasPriceData {
					fmt.Printf(" ✅ $%s\n", enriched.USDFormatted)
				} else {
					fmt.Printf(" ⚪ No price data\n")
				}
			}
		} else if m.verbose {
			fmt.Printf(" ❌ Failed to enrich\n")
		}
	}

	if m.verbose {
		fmt.Printf("✅ Successfully enriched %d/%d amounts\n", successCount, len(detectedAmounts))
		fmt.Println(strings.Repeat("💰", 60) + "\n")
	}

	// Add enriched amounts to baggage
	baggage["enriched_amounts"] = enrichedAmounts

	// Sub-step 5: Enriching raw transaction data
	if progressTracker != nil {
		progressTracker.UpdateComponent(
			"monetary_value_enricher",
			models.ComponentGroupEnrichment,
			"Calculating USD Values",
			models.ComponentStatusRunning,
			"Adding USD values to transaction data...",
		)
	}

	if m.verbose {
		fmt.Println("📝 Sub-step 5: Adding USD values to transaction data...")
	}

	// Enrich raw data with gas fees (unchanged - still needed)
	if err := m.enrichRawData(baggage, nativeTokenPrice); err != nil {
		return fmt.Errorf("failed to enrich raw data: %w", err)
	}

	// Final progress update
	if progressTracker != nil {
		progressTracker.UpdateComponent(
			"monetary_value_enricher",
			models.ComponentGroupEnrichment,
			"Calculating USD Values",
			models.ComponentStatusRunning,
			"USD value calculations completed successfully",
		)
	}

	if m.verbose {
		fmt.Println("🎯 Sub-step 6: USD value calculations completed successfully")
		fmt.Println("=== MONETARY VALUE ENRICHER: COMPLETED PROCESSING ===")
	}
	return nil
}

// enrichDetectedAmount converts a detected amount to an enriched amount with USD values
func (m *MonetaryValueEnricher) enrichDetectedAmount(detected DetectedAmount, tokenPrices map[string]*TokenPrice, tokenMetadata map[string]*TokenMetadata, hasPrices bool, baggage map[string]interface{}) *EnrichedAmount {
	var formattedAmount float64
	var decimals int
	var tokenSymbol string
	var contractAddress string

	// Handle native token gas fees
	if detected.TokenContract == "native" {
		// This is a native token (like ETH for gas fees)
		// Get network ID from baggage to determine native token
		rawData, ok := baggage["raw_data"].(map[string]interface{})
		if !ok {
			return nil
		}

		networkID, ok := rawData["network_id"].(float64)
		if !ok {
			return nil
		}

		// Get native token symbol using centralized client
		nativeSymbol := m.cmcClient.GetNativeTokenSymbol(int64(networkID))
		if nativeSymbol == "" {
			return nil // Can't process without symbol
		}

		tokenSymbol = nativeSymbol
		contractAddress = "native"
		decimals = 18 // Native tokens typically use 18 decimals

		// Convert amount (assume it's in wei for native tokens)
		formattedAmount = m.convertAmountToTokens(detected.Amount, decimals)
		if formattedAmount == 0 {
			return nil
		}
	} else {
		// Regular ERC20 token - get metadata for decimal conversion
		metadata, exists := tokenMetadata[strings.ToLower(detected.TokenContract)]
		if !exists {
			return nil // Can't convert without decimals
		}

		// Convert raw amount to formatted amount
		formattedAmount = m.convertAmountToTokens(detected.Amount, metadata.Decimals)
		if formattedAmount == 0 {
			return nil // Skip zero amounts
		}

		decimals = metadata.Decimals
		tokenSymbol = detected.TokenSymbol
		contractAddress = detected.TokenContract
	}

	// Format the amount for display
	formattedStr := m.formatAmount(formattedAmount)

	// Create enriched amount with basic info
	enriched := &EnrichedAmount{
		DetectedAmount:  detected,
		FormattedAmount: formattedStr,
		TokenDecimals:   decimals,
		HasPriceData:    false,
	}

	// Add USD value if price data is available
	if hasPrices {
		var pricePerToken float64
		var priceSource string

		if detected.TokenContract == "native" {
			// For native tokens, get price by symbol
			for _, price := range tokenPrices {
				// Check if this price matches our native token symbol
				if strings.EqualFold(price.Symbol, tokenSymbol) {
					pricePerToken = price.Price
					priceSource = price.PriceSource
					break
				}
			}
		} else {
			// For ERC20 tokens, get price by contract address
			if price, exists := tokenPrices[strings.ToLower(contractAddress)]; exists && price.Price > 0 {
				pricePerToken = price.Price
				priceSource = price.PriceSource
			}
		}

		if pricePerToken > 0 {
			usdValue := formattedAmount * pricePerToken
			enriched.USDValue = usdValue
			enriched.USDFormatted = fmt.Sprintf("%.2f", usdValue)
			enriched.PricePerToken = pricePerToken
			enriched.PriceSource = priceSource
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
	nativeTokenSymbol := m.cmcClient.GetNativeTokenSymbol(int64(networkID))
	if nativeTokenSymbol == "" || !m.cmcClient.IsAvailable() {
		// No native token symbol or API key - return 0
		return 0
	}

	// Fetch actual price from centralized CoinMarketCap client
	price, err := m.cmcClient.GetNativeTokenPrice(ctx, nativeTokenSymbol)
	if err != nil {
		// If price fetch fails, return 0 instead of hardcoded fallback
		return 0
	}

	return price
}

// getNativeTokenSymbol returns the native token symbol for a given network
// Uses network configuration and pattern detection instead of hardcoding chain IDs
func (m *MonetaryValueEnricher) getNativeTokenSymbol(networkID int64) string {
	return m.cmcClient.GetNativeTokenSymbol(networkID)
}

// getFallbackNativeTokenPrice returns generic fallback price
// Uses RPC-first approach, then API fallbacks, no hardcoded prices
func (m *MonetaryValueEnricher) getFallbackNativeTokenPrice(networkID int64) float64 {
	// Generic approach: try to fetch current price via API for any network
	nativeSymbol := m.cmcClient.GetNativeTokenSymbol(networkID)
	if nativeSymbol != "" && m.cmcClient.IsAvailable() {
		// Try to fetch actual current price from API
		if price, err := m.cmcClient.GetNativeTokenPrice(context.Background(), nativeSymbol); err == nil {
			return price
		}
	}

	// No hardcoded prices - return 0 to indicate price unavailable
	// This is better than hardcoding outdated prices
	// The system will work without USD values but still show token amounts
	return 0
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

// GetRagContext provides RAG context for monetary value information (minimal for this tool)
func (m *MonetaryValueEnricher) GetRagContext(ctx context.Context, baggage map[string]interface{}) *RagContext {
	ragContext := NewRagContext()
	// Monetary value enricher provides current market calculations, not historical knowledge for RAG
	return ragContext
}
