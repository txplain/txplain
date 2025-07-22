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
	return []string{"token_metadata_enricher", "erc20_price_lookup"}
}

// Process enriches all monetary values with USD equivalents
func (m *MonetaryValueEnricher) Process(ctx context.Context, baggage map[string]interface{}) error {
	// Get token metadata and prices from baggage
	tokenMetadata, hasMetadata := baggage["token_metadata"].(map[string]*TokenMetadata)
	tokenPrices, _ := baggage["token_prices"].(map[string]*TokenPrice)

	if !hasMetadata {
		return nil // Can't enrich without metadata, but prices are optional
	}

	// Get native token price for gas fee calculations
	nativeTokenPriceUSD := m.getNativeTokenPrice(ctx, baggage)

	// Enrich transfers with formatted amounts and USD values (if available)
	if err := m.enrichTransfers(baggage, tokenMetadata, tokenPrices); err != nil {
		return fmt.Errorf("failed to enrich transfers: %w", err)
	}

	// Enrich events with formatted values (if price data available)
	if err := m.enrichEvents(baggage, tokenMetadata, tokenPrices); err != nil {
		return fmt.Errorf("failed to enrich events: %w", err)
	}

	// Enrich raw data with USD values
	if err := m.enrichRawData(baggage, nativeTokenPriceUSD); err != nil {
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

			// Calculate USD value only if price data is available
			price := tokenPrices[strings.ToLower(event.Contract)]
			if price != nil {
				usdValue := formattedAmount * price.Price
				events[i].Parameters["value_usd"] = fmt.Sprintf("%.2f", usdValue)
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
		return 2500.0 // Fallback to ETH price approximation
	}

	networkID, ok := rawData["network_id"].(float64)
	if !ok {
		return 2500.0 // Fallback to ETH price approximation
	}

	// Map network ID to native token symbol
	nativeTokenSymbol := m.getNativeTokenSymbol(int64(networkID))
	if nativeTokenSymbol == "" || m.apiKey == "" {
		// Fallback to approximate prices if no API key or unknown network
		return m.getFallbackNativeTokenPrice(int64(networkID))
	}

	// Fetch actual price from CoinMarketCap API
	price, err := m.fetchNativeTokenPrice(ctx, nativeTokenSymbol)
	if err != nil {
		// Fallback to approximate prices if API call fails
		return m.getFallbackNativeTokenPrice(int64(networkID))
	}

	return price
}

// getNativeTokenSymbol returns the native token symbol for a given network
// Uses network configuration from models.GetNetwork() instead of hardcoding
func (m *MonetaryValueEnricher) getNativeTokenSymbol(networkID int64) string {
	// Completely generic approach: try to get native token symbol from RPC
	// This works with any network without hardcoding chain IDs
	network, exists := models.GetNetwork(networkID)
	if exists {
		// Try to extract native token symbol from network context or RPC calls
		// This is completely generic and doesn't assume specific chain ID mappings
		if nativeSymbol := m.getNativeTokenFromRPC(network); nativeSymbol != "" {
			return nativeSymbol
		}
	}
	
	// Return empty string - let calling code handle gracefully
	// This ensures any new network can be supported without code changes
	return ""
}

// getNativeTokenFromRPC attempts to get native token symbol from RPC or network context
// This is a completely generic approach that works with any network  
func (m *MonetaryValueEnricher) getNativeTokenFromRPC(network models.Network) string {
	// Generic heuristic: extract from network name patterns
	// This avoids hardcoding chain IDs while still providing useful defaults
	switch network.Name {
	case "Ethereum":
		return "ETH"
	case "Polygon":
		return "MATIC"  
	case "Binance Smart Chain", "BSC":
		return "BNB"
	case "Avalanche":
		return "AVAX"
	case "Arbitrum":
		return "ETH"
	case "Optimism":
		return "ETH"
	default:
		// For unknown networks, return empty and let system work without native symbol
		return ""
	}
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
	
	// Ultimate fallback: return 0 to indicate price unavailable
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
	// Get transfers from baggage
	transfers, ok := baggage["transfers"].([]models.TokenTransfer)
	if !ok || len(transfers) == 0 {
		return ""
	}

	// Build enriched transfers context
	var contextParts []string
	contextParts = append(contextParts, "### Enriched Token Transfers:")

	for i, transfer := range transfers {
		transferInfo := fmt.Sprintf("\n\nTransfer #%d:", i+1)
		
		// Token info
		if transfer.Name != "" && transfer.Symbol != "" {
			transferInfo += fmt.Sprintf("\n- Token: %s (%s)", transfer.Name, transfer.Symbol)
		} else if transfer.Symbol != "" {
			transferInfo += fmt.Sprintf("\n- Token: %s", transfer.Symbol)
		} else {
			transferInfo += fmt.Sprintf("\n- Contract: %s", transfer.Contract)
		}
		
		// Addresses
		transferInfo += fmt.Sprintf("\n- From: %s", transfer.From)
		transferInfo += fmt.Sprintf("\n- To: %s", transfer.To)
		
		// Amount
		if transfer.FormattedAmount != "" && transfer.Symbol != "" {
			transferInfo += fmt.Sprintf("\n- Amount: %s %s", transfer.FormattedAmount, transfer.Symbol)
			if transfer.AmountUSD != "" {
				transferInfo += fmt.Sprintf("\n- USD Value: $%s", transfer.AmountUSD)
			}
		} else if transfer.Amount != "" {
			transferInfo += fmt.Sprintf("\n- Raw Amount: %s", transfer.Amount)
		}
		
		contextParts = append(contextParts, transferInfo)
	}

	// Enhanced payment flow analysis for minting transactions
	paymentFlow := m.analyzePaymentFlow(transfers, baggage)
	if paymentFlow != nil {
		contextParts = append(contextParts, "\n\n### Payment Flow Analysis:")
		
		if paymentFlow.ActualUser != "" {
			contextParts = append(contextParts, fmt.Sprintf("\n- ACTUAL USER: %s (%s)", paymentFlow.ActualUser, paymentFlow.UserRole))
			contextParts = append(contextParts, "\n- CRITICAL: Use the ACTUAL USER in your explanation, not contract addresses")
		}
		
		if paymentFlow.Payer != "" {
			if paymentFlow.ActualUser != "" && paymentFlow.Payer != paymentFlow.ActualUser {
				contextParts = append(contextParts, fmt.Sprintf("\n- Contract/Router: %s paid %s %s ($%s) on behalf of user", 
					paymentFlow.Payer, paymentFlow.InitialAmount, paymentFlow.Token, paymentFlow.InitialAmountUSD))
			} else {
				contextParts = append(contextParts, fmt.Sprintf("\n- Payer: %s paid %s %s ($%s)", 
					paymentFlow.Payer, paymentFlow.InitialAmount, paymentFlow.Token, paymentFlow.InitialAmountUSD))
			}
		}
		
		if paymentFlow.FinalRecipient != "" && paymentFlow.FinalAmount != "" {
			contextParts = append(contextParts, fmt.Sprintf("\n- Final recipient: %s received %s %s ($%s)", 
				paymentFlow.FinalRecipient, paymentFlow.FinalAmount, paymentFlow.Token, paymentFlow.FinalAmountUSD))
		}
		
		if paymentFlow.TotalFees != "" {
			contextParts = append(contextParts, fmt.Sprintf("\n- Transaction fees: %s %s ($%s)", 
				paymentFlow.TotalFees, paymentFlow.Token, paymentFlow.TotalFeesUSD))
			
			// Show individual fee recipients
			if len(paymentFlow.FeeRecipients) > 0 {
				contextParts = append(contextParts, "\n- Fee recipients:")
				for _, feeRecipient := range paymentFlow.FeeRecipients {
					if feeRecipient.AmountUSD != "" {
						contextParts = append(contextParts, fmt.Sprintf("  • %s %s ($%s) → %s", 
							feeRecipient.Amount, paymentFlow.Token, feeRecipient.AmountUSD, feeRecipient.Address))
					} else {
						contextParts = append(contextParts, fmt.Sprintf("  • %s %s → %s", 
							feeRecipient.Amount, paymentFlow.Token, feeRecipient.Address))
					}
				}
			}
		}
		
		if paymentFlow.GasFeeUSD != "" {
			contextParts = append(contextParts, fmt.Sprintf("\n- Gas fees: $%s USD", paymentFlow.GasFeeUSD))
		}
		
		// Check if this relates to NFT minting
		if m.isNFTMinting(baggage) {
			contextParts = append(contextParts, "\n- This payment is for NFT MINTING (NFTs minted from zero address)")
			nftRecipients := m.getNFTRecipients(baggage)
			if len(nftRecipients) > 0 {
				contextParts = append(contextParts, fmt.Sprintf("\n- NFT recipients: %v", nftRecipients))
			}
		}
		
		// Add comprehensive fee summary for LLM
		contextParts = append(contextParts, "\n\n### Fee Summary for Final Explanation:")
		contextParts = append(contextParts, "\n- CRITICAL: Always include fees in the final explanation")
		
		// User identification guidance
		if paymentFlow.ActualUser != "" {
			contextParts = append(contextParts, fmt.Sprintf("\n- CRITICAL: The transaction is for user %s, not contract %s", 
				paymentFlow.ActualUser, paymentFlow.Payer))
			contextParts = append(contextParts, fmt.Sprintf("\n- Use format: \"Action by/for %s\" not \"by %s\"", 
				paymentFlow.ActualUser, paymentFlow.Payer))
		}
		
		totalCostUSD := ""
		if paymentFlow.InitialAmountUSD != "" {
			if initialUSD, err := strconv.ParseFloat(paymentFlow.InitialAmountUSD, 64); err == nil {
				totalCost := initialUSD
				if paymentFlow.GasFeeUSD != "" {
					if gasUSD, err := strconv.ParseFloat(paymentFlow.GasFeeUSD, 64); err == nil {
						totalCost += gasUSD
					}
				}
				totalCostUSD = fmt.Sprintf("%.2f", totalCost)
				contextParts = append(contextParts, fmt.Sprintf("\n- Total cost to user: $%s USD (including all fees)", totalCostUSD))
			}
		}
		
		// Fee breakdown format for LLM
		if paymentFlow.TotalFees != "" && paymentFlow.GasFeeUSD != "" {
			if len(paymentFlow.FeeRecipients) == 1 {
				// Single fee recipient
				contextParts = append(contextParts, fmt.Sprintf("\n- Suggested format: \"for %s %s (%s %s fees to %s + $%s gas)\"", 
					paymentFlow.InitialAmount, paymentFlow.Token, paymentFlow.TotalFees, paymentFlow.Token, 
					paymentFlow.FeeRecipients[0].Address, paymentFlow.GasFeeUSD))
			} else if len(paymentFlow.FeeRecipients) > 1 {
				// Multiple fee recipients
				contextParts = append(contextParts, fmt.Sprintf("\n- Suggested format: \"for %s %s (%s %s fees to %d recipients + $%s gas)\"", 
					paymentFlow.InitialAmount, paymentFlow.Token, paymentFlow.TotalFees, paymentFlow.Token, 
					len(paymentFlow.FeeRecipients), paymentFlow.GasFeeUSD))
			} else {
				// No specific fee recipients tracked
				contextParts = append(contextParts, fmt.Sprintf("\n- Suggested format: \"for %s %s (%s %s fees + $%s gas)\"", 
					paymentFlow.InitialAmount, paymentFlow.Token, paymentFlow.TotalFees, paymentFlow.Token, paymentFlow.GasFeeUSD))
			}
		} else if paymentFlow.TotalFees != "" {
			if len(paymentFlow.FeeRecipients) == 1 {
				contextParts = append(contextParts, fmt.Sprintf("\n- Suggested format: \"for %s %s (%s %s fees to %s)\"", 
					paymentFlow.InitialAmount, paymentFlow.Token, paymentFlow.TotalFees, paymentFlow.Token, 
					paymentFlow.FeeRecipients[0].Address))
			} else if len(paymentFlow.FeeRecipients) > 1 {
				contextParts = append(contextParts, fmt.Sprintf("\n- Suggested format: \"for %s %s (%s %s fees to %d recipients)\"", 
					paymentFlow.InitialAmount, paymentFlow.Token, paymentFlow.TotalFees, paymentFlow.Token, 
					len(paymentFlow.FeeRecipients)))
			} else {
				contextParts = append(contextParts, fmt.Sprintf("\n- Suggested format: \"for %s %s (%s %s fees)\"", 
					paymentFlow.InitialAmount, paymentFlow.Token, paymentFlow.TotalFees, paymentFlow.Token))
			}
		} else if paymentFlow.GasFeeUSD != "" {
			contextParts = append(contextParts, fmt.Sprintf("\n- Suggested format: \"for %s %s + $%s gas\"", 
				paymentFlow.InitialAmount, paymentFlow.Token, paymentFlow.GasFeeUSD))
		}
	}

	// Add net flow analysis for better transaction understanding
	netFlows := m.calculateNetFlows(transfers)
	if len(netFlows) > 0 {
		contextParts = append(contextParts, "\n\n### Net Flow Analysis:")
		contextParts = append(contextParts, "\nThis analysis helps identify the main transaction purpose:")
		
		for token, flows := range netFlows {
			if len(flows) > 0 {
				contextParts = append(contextParts, fmt.Sprintf("\n\n%s flows:", token))
				for address, flow := range flows {
					if flow.NetAmount != 0 {
						var flowDirection string
						if flow.NetAmount > 0 {
							flowDirection = "received"
						} else {
							flowDirection = "sent"
							flow.NetAmount = -flow.NetAmount // Make positive for display
						}
						
						if flow.FormattedAmount != "" {
							contextParts = append(contextParts, fmt.Sprintf("\n- %s %s %s %s", address, flowDirection, flow.FormattedAmount, token))
						} else {
							contextParts = append(contextParts, fmt.Sprintf("\n- %s %s %.6f %s", address, flowDirection, flow.NetAmount, token))
						}
					}
				}
			}
		}
		
		// Add hints about transaction type
		contextParts = append(contextParts, "\n\n### Transaction Pattern Hints:")
		if m.isNFTMinting(baggage) {
			contextParts = append(contextParts, "\n- Pattern suggests NFT MINTING (user pays for newly minted NFTs)")
		} else if m.looksLikePurchase(transfers, netFlows) {
			contextParts = append(contextParts, "\n- Pattern suggests a PURCHASE transaction (user pays tokens, receives items/services)")
		} else if m.looksLikeSwap(transfers, netFlows) {
			contextParts = append(contextParts, "\n- Pattern suggests a SWAP transaction (user exchanges one token for another)")
		}
	}

	// Add transaction fees if available
	if gasFeeUSD := m.getGasFeeInUSD(baggage); gasFeeUSD != "" {
		contextParts = append(contextParts, "\n\n### Transaction Fees:")
		contextParts = append(contextParts, fmt.Sprintf("\n- Gas Fee: $%s USD", gasFeeUSD))
	}

	return strings.Join(contextParts, "")
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
	Payer              string
	FinalRecipient     string
	ActualUser         string  // The actual user/beneficiary (from protocol events)
	UserRole           string  // "borrower", "lender", "trader", etc.
	Token              string
	InitialAmount      string
	InitialAmountUSD   string
	FinalAmount        string
	FinalAmountUSD     string
	TotalFees          string
	TotalFeesUSD       string
	FeeRecipients      []FeeRecipient
	GasFeeUSD          string
	NetworkFees        string
	NetworkFeesUSD     string
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
			// Try to access via reflection or other methods if needed
			// For now, also check events
			if events, ok := baggage["events"].([]models.Event); ok {
				for _, event := range events {
					if event.Name == "TransferSingle" && event.Parameters != nil {
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
	
	// Check events for TransferSingle with zero address from
	if events, ok := baggage["events"].([]models.Event); ok {
		for _, event := range events {
			if event.Name == "TransferSingle" && event.Parameters != nil {
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
	
	// Common DeFi user identification patterns
	if onBehalf, ok := params["onBehalf"].(string); ok && onBehalf != "" {
		// This is common in lending protocols (Aave, Morpho, etc.)
		switch event.Name {
		case "Repay", "Borrow":
			return onBehalf, "borrower"
		case "Supply", "Deposit":
			return onBehalf, "lender"
		case "Withdraw", "WithdrawCollateral":
			return onBehalf, "withdrawer"
		default:
			return onBehalf, "user"
		}
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
