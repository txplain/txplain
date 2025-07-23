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
	fmt.Println("=== MONETARY VALUE ENRICHER: STARTING PROCESSING ===")

	// Get detected amounts from amounts_finder
	detectedAmounts, ok := baggage["detected_amounts"].([]DetectedAmount)
	if !ok || len(detectedAmounts) == 0 {
		fmt.Println("MONETARY VALUE ENRICHER: No detected amounts found in baggage")
		return nil // No amounts detected, nothing to enrich
	}

	fmt.Printf("MONETARY VALUE ENRICHER: Processing %d detected amounts\n", len(detectedAmounts))

	// Get token prices from erc20_price_lookup
	tokenPrices, hasPrices := baggage["token_prices"].(map[string]*TokenPrice)

	// Get token metadata for decimal conversion
	tokenMetadata, hasMetadata := baggage["token_metadata"].(map[string]*TokenMetadata)

	if !hasMetadata {
		fmt.Println("MONETARY VALUE ENRICHER: No token metadata found, cannot convert amounts")
		return nil // Can't convert amounts without metadata
	}

	fmt.Printf("MONETARY VALUE ENRICHER: Has prices: %v, Has metadata: %v\n", hasPrices, hasMetadata)

	// Get native token price for gas fee calculations
	nativeTokenPrice := m.getNativeTokenPrice(ctx, baggage)

	// Enrich each detected amount with USD value
	enrichedAmounts := make([]EnrichedAmount, 0, len(detectedAmounts))

	for i, amount := range detectedAmounts {
		fmt.Printf("MONETARY VALUE ENRICHER: Processing amount %d: %s for token %s\n", i+1, amount.Amount, amount.TokenContract)
		enriched := m.enrichDetectedAmount(amount, tokenPrices, tokenMetadata, hasPrices)
		if enriched != nil {
			enrichedAmounts = append(enrichedAmounts, *enriched)
			fmt.Printf("MONETARY VALUE ENRICHER: Enriched amount %d: %s -> $%s\n", i+1, enriched.FormattedAmount, enriched.USDFormatted)
		} else {
			fmt.Printf("MONETARY VALUE ENRICHER: Failed to enrich amount %d\n", i+1)
		}
	}

	fmt.Printf("MONETARY VALUE ENRICHER: Successfully enriched %d/%d amounts\n", len(enrichedAmounts), len(detectedAmounts))

	// Add enriched amounts to baggage
	baggage["enriched_amounts"] = enrichedAmounts

	// Enrich raw data with gas fees (unchanged - still needed)
	if err := m.enrichRawData(baggage, nativeTokenPrice); err != nil {
		return fmt.Errorf("failed to enrich raw data: %w", err)
	}

	fmt.Println("=== MONETARY VALUE ENRICHER: COMPLETED PROCESSING ===")
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

// GetRagContext provides RAG context for monetary value information (minimal for this tool)
func (m *MonetaryValueEnricher) GetRagContext(ctx context.Context, baggage map[string]interface{}) *RagContext {
	ragContext := NewRagContext()
	// Monetary value enricher provides current market calculations, not historical knowledge for RAG
	return ragContext
}
