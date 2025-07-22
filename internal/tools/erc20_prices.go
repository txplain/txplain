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
)

// ERC20PriceLookup fetches token prices from CoinMarketCap API
type ERC20PriceLookup struct {
	apiKey     string
	httpClient *http.Client
}

// TokenPrice represents price information for a token
type TokenPrice struct {
	ID                int                    `json:"id"`
	Name              string                 `json:"name"`
	Symbol            string                 `json:"symbol"`
	Slug              string                 `json:"slug"`
	Contract          string                 `json:"contract_address,omitempty"`
	Platform          string                 `json:"platform,omitempty"`
	Price             float64                `json:"price"`
	PriceChange24h    float64                `json:"price_change_24h"`
	PriceChangePct24h float64                `json:"price_change_pct_24h"`
	MarketCap         float64                `json:"market_cap"`
	Volume24h         float64                `json:"volume_24h"`
	LastUpdated       time.Time              `json:"last_updated"`
	Quote             map[string]interface{} `json:"quote,omitempty"`
	// New fields for transfer calculations
	TransferAmounts map[string]float64 `json:"transfer_amounts,omitempty"` // transfer_id -> amount in tokens
	TransferValues  map[string]float64 `json:"transfer_values,omitempty"`  // transfer_id -> USD value
}

// CoinMarketCapMapResponse represents the response from /v1/cryptocurrency/map
type CoinMarketCapMapResponse struct {
	Status struct {
		Timestamp    string `json:"timestamp"`
		ErrorCode    int    `json:"error_code"`
		ErrorMessage string `json:"error_message"`
		Elapsed      int    `json:"elapsed"`
		CreditCount  int    `json:"credit_count"`
	} `json:"status"`
	Data []struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		Symbol   string `json:"symbol"`
		Slug     string `json:"slug"`
		IsActive int    `json:"is_active"`
		Platform struct {
			ID           int    `json:"id"`
			Name         string `json:"name"`
			Symbol       string `json:"symbol"`
			Slug         string `json:"slug"`
			TokenAddress string `json:"token_address"`
		} `json:"platform,omitempty"`
		FirstHistoricalData string `json:"first_historical_data"`
		LastHistoricalData  string `json:"last_historical_data"`
	} `json:"data"`
}

// CoinMarketCapQuoteResponse represents the response from /v1/cryptocurrency/quotes/latest
type CoinMarketCapQuoteResponse struct {
	Status struct {
		Timestamp    string `json:"timestamp"`
		ErrorCode    int    `json:"error_code"`
		ErrorMessage string `json:"error_message"`
		Elapsed      int    `json:"elapsed"`
		CreditCount  int    `json:"credit_count"`
	} `json:"status"`
	Data map[string]struct {
		ID     int    `json:"id"`
		Name   string `json:"name"`
		Symbol string `json:"symbol"`
		Slug   string `json:"slug"`
		Quote  map[string]struct {
			Price                 float64 `json:"price"`
			Volume24h             float64 `json:"volume_24h"`
			VolumeChange24h       float64 `json:"volume_change_24h"`
			PercentChange1h       float64 `json:"percent_change_1h"`
			PercentChange24h      float64 `json:"percent_change_24h"`
			PercentChange7d       float64 `json:"percent_change_7d"`
			PercentChange30d      float64 `json:"percent_change_30d"`
			PercentChange60d      float64 `json:"percent_change_60d"`
			PercentChange90d      float64 `json:"percent_change_90d"`
			MarketCap             float64 `json:"market_cap"`
			MarketCapDominance    float64 `json:"market_cap_dominance"`
			FullyDilutedMarketCap float64 `json:"fully_diluted_market_cap"`
			LastUpdated           string  `json:"last_updated"`
		} `json:"quote"`
	} `json:"data"`
}

// NewERC20PriceLookup creates a new ERC20 price lookup tool
func NewERC20PriceLookup(apiKey string) *ERC20PriceLookup {
	return &ERC20PriceLookup{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Name returns the tool name
func (t *ERC20PriceLookup) Name() string {
	return "erc20_price_lookup"
}

// Description returns the tool description
func (t *ERC20PriceLookup) Description() string {
	return "Looks up ERC20 token prices using CoinMarketCap API. Supports lookup by symbol, name, or contract address."
}

// Dependencies returns the tools this processor depends on
func (t *ERC20PriceLookup) Dependencies() []string {
	return []string{"token_metadata_enricher"} // Needs token metadata for optimal lookups
}

// Process adds token price information to baggage
func (t *ERC20PriceLookup) Process(ctx context.Context, baggage map[string]interface{}) error {
	if t.apiKey == "" {
		return nil // No API key, skip price lookup
	}

	// Get token metadata from baggage
	tokenMetadata, ok := baggage["token_metadata"].(map[string]*TokenMetadata)
	if !ok || len(tokenMetadata) == 0 {
		return nil // No token metadata, nothing to price
	}

	// Look up prices for each token
	tokenPrices := make(map[string]*TokenPrice)
	for address, metadata := range tokenMetadata {
		if metadata.Type == "ERC20" {
			priceInput := map[string]interface{}{
				"contract_address": address,
			}
			if metadata.Symbol != "" {
				priceInput["symbol"] = metadata.Symbol
			}

			result, err := t.Run(ctx, priceInput)
			if err == nil {
				if tokenData, ok := result["token"].(*TokenPrice); ok {
					tokenData.Contract = address // Ensure contract address is set
					tokenPrices[address] = tokenData
				}
			}
		}
	}

	// Calculate USD values for transfers if available
	t.calculateTransferValues(baggage, tokenPrices, tokenMetadata)

	// Add token prices to baggage
	baggage["token_prices"] = tokenPrices
	return nil
}

// calculateTransferValues calculates USD values for ERC20 transfers
func (t *ERC20PriceLookup) calculateTransferValues(baggage map[string]interface{}, tokenPrices map[string]*TokenPrice, tokenMetadata map[string]*TokenMetadata) {
	// Get transfers from baggage
	transfers, ok := baggage["transfers"]
	if !ok {
		return
	}

	for address, price := range tokenPrices {
		metadata := tokenMetadata[address]
		if metadata == nil {
			continue
		}

		price.TransferAmounts = make(map[string]float64)
		price.TransferValues = make(map[string]float64)

		// Handle transfers as TokenTransfer slice or interface slice
		var tokenTransfers []interface{}
		switch v := transfers.(type) {
		case []interface{}:
			tokenTransfers = v
		default:
			continue
		}

		// Find transfers for this token
		for i, transferData := range tokenTransfers {
			var transfer map[string]interface{}

			// Convert to map for easier access
			switch v := transferData.(type) {
			case map[string]interface{}:
				transfer = v
			default:
				// Try to convert struct to map using JSON marshaling/unmarshaling
				if data, err := json.Marshal(transferData); err == nil {
					var mapped map[string]interface{}
					if err := json.Unmarshal(data, &mapped); err == nil {
						transfer = mapped
					}
				}
				if transfer == nil {
					continue
				}
			}

			// Check if this transfer is for our token
			tokenContract, _ := transfer["contract"].(string)
			if strings.EqualFold(tokenContract, address) {
				transferID := fmt.Sprintf("transfer_%d", i)

				// Get transfer amount
				amountStr, ok := transfer["amount"].(string)
				if !ok || amountStr == "" {
					continue
				}

				// Convert amount to actual token units
				tokenAmount := t.convertAmountToTokens(amountStr, metadata.Decimals)
				if tokenAmount > 0 {
					usdValue := tokenAmount * price.Price
					price.TransferAmounts[transferID] = tokenAmount
					price.TransferValues[transferID] = usdValue
				}
			}
		}
	}
}

// convertAmountToTokens converts a raw amount (usually in wei-like units) to token units
func (t *ERC20PriceLookup) convertAmountToTokens(amountStr string, decimals int) float64 {
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

// Run executes the price lookup
func (t *ERC20PriceLookup) Run(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
	// Extract search parameters from input
	symbol, _ := input["symbol"].(string)
	name, _ := input["name"].(string)
	contractAddress, _ := input["contract_address"].(string)
	convert, _ := input["convert"].(string) // Currency to convert to (default: USD)

	if convert == "" {
		convert = "USD"
	}

	// Need at least one search parameter
	if symbol == "" && name == "" && contractAddress == "" {
		return nil, NewToolError("erc20_price_lookup", "must provide symbol, name, or contract_address", "MISSING_PARAMS")
	}

	// Step 1: Find the token using the map endpoint
	tokenID, err := t.findToken(ctx, symbol, name, contractAddress)
	if err != nil {
		return nil, NewToolError("erc20_price_lookup", fmt.Sprintf("failed to find token: %v", err), "TOKEN_NOT_FOUND")
	}

	// Step 2: Get price information
	priceInfo, err := t.getTokenPrice(ctx, tokenID, convert)
	if err != nil {
		return nil, NewToolError("erc20_price_lookup", fmt.Sprintf("failed to get price: %v", err), "PRICE_FETCH_ERROR")
	}

	return map[string]interface{}{
		"token": priceInfo,
	}, nil
}

// findToken searches for a token using CoinMarketCap's map endpoint
func (t *ERC20PriceLookup) findToken(ctx context.Context, symbol, name, contractAddress string) (int, error) {
	if t.apiKey == "" {
		return 0, fmt.Errorf("CoinMarketCap API key not configured")
	}

	// Build query parameters
	params := url.Values{}
	params.Set("listing_status", "active")
	params.Set("limit", "5000") // Get more results for better matching

	if symbol != "" {
		params.Set("symbol", strings.ToUpper(symbol))
	}

	// Construct URL
	mapURL := fmt.Sprintf("https://pro-api.coinmarketcap.com/v1/cryptocurrency/map?%s", params.Encode())

	// Make request
	req, err := http.NewRequestWithContext(ctx, "GET", mapURL, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-CMC_PRO_API_KEY", t.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := t.httpClient.Do(req)
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

	var mapResp CoinMarketCapMapResponse
	if err := json.Unmarshal(body, &mapResp); err != nil {
		return 0, fmt.Errorf("failed to parse response: %w", err)
	}

	if mapResp.Status.ErrorCode != 0 {
		return 0, fmt.Errorf("API error: %s", mapResp.Status.ErrorMessage)
	}

	// Find best match
	bestMatch := t.findBestMatch(mapResp.Data, symbol, name, contractAddress)
	if bestMatch == nil {
		return 0, fmt.Errorf("no matching token found")
	}

	return bestMatch.ID, nil
}

// findBestMatch finds the best matching token from the search results
func (t *ERC20PriceLookup) findBestMatch(tokens []struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Symbol   string `json:"symbol"`
	Slug     string `json:"slug"`
	IsActive int    `json:"is_active"`
	Platform struct {
		ID           int    `json:"id"`
		Name         string `json:"name"`
		Symbol       string `json:"symbol"`
		Slug         string `json:"slug"`
		TokenAddress string `json:"token_address"`
	} `json:"platform,omitempty"`
	FirstHistoricalData string `json:"first_historical_data"`
	LastHistoricalData  string `json:"last_historical_data"`
}, symbol, name, contractAddress string) *struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Symbol   string `json:"symbol"`
	Slug     string `json:"slug"`
	IsActive int    `json:"is_active"`
	Platform struct {
		ID           int    `json:"id"`
		Name         string `json:"name"`
		Symbol       string `json:"symbol"`
		Slug         string `json:"slug"`
		TokenAddress string `json:"token_address"`
	} `json:"platform,omitempty"`
	FirstHistoricalData string `json:"first_historical_data"`
	LastHistoricalData  string `json:"last_historical_data"`
} {

	// First priority: exact contract address match
	if contractAddress != "" {
		contractAddress = strings.ToLower(contractAddress)
		for _, token := range tokens {
			if token.IsActive == 1 && strings.ToLower(token.Platform.TokenAddress) == contractAddress {
				return &token
			}
		}
	}

	// Second priority: exact symbol match (active tokens only)
	if symbol != "" {
		symbol = strings.ToUpper(symbol)
		for _, token := range tokens {
			if token.IsActive == 1 && strings.ToUpper(token.Symbol) == symbol {
				// Prefer tokens with contract addresses (ERC20) if we're looking for ERC20
				if contractAddress != "" || token.Platform.TokenAddress != "" {
					return &token
				}
			}
		}
		// Fallback to any exact symbol match
		for _, token := range tokens {
			if token.IsActive == 1 && strings.ToUpper(token.Symbol) == symbol {
				return &token
			}
		}
	}

	// Third priority: exact name match
	if name != "" {
		nameLower := strings.ToLower(name)
		for _, token := range tokens {
			if token.IsActive == 1 && strings.ToLower(token.Name) == nameLower {
				return &token
			}
		}
	}

	// Fourth priority: partial name match
	if name != "" {
		nameLower := strings.ToLower(name)
		for _, token := range tokens {
			if token.IsActive == 1 && strings.Contains(strings.ToLower(token.Name), nameLower) {
				return &token
			}
		}
	}

	return nil
}

// getTokenPrice fetches the latest price for a token by ID
func (t *ERC20PriceLookup) getTokenPrice(ctx context.Context, tokenID int, convert string) (*TokenPrice, error) {
	// Build query parameters
	params := url.Values{}
	params.Set("id", strconv.Itoa(tokenID))
	params.Set("convert", convert)

	// Construct URL
	quoteURL := fmt.Sprintf("https://pro-api.coinmarketcap.com/v1/cryptocurrency/quotes/latest?%s", params.Encode())

	// Make request
	req, err := http.NewRequestWithContext(ctx, "GET", quoteURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-CMC_PRO_API_KEY", t.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var quoteResp CoinMarketCapQuoteResponse
	if err := json.Unmarshal(body, &quoteResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if quoteResp.Status.ErrorCode != 0 {
		return nil, fmt.Errorf("API error: %s", quoteResp.Status.ErrorMessage)
	}

	// Extract token data
	tokenIDStr := strconv.Itoa(tokenID)
	tokenData, exists := quoteResp.Data[tokenIDStr]
	if !exists {
		return nil, fmt.Errorf("token data not found in response")
	}

	// Extract quote data for the requested currency
	quoteData, exists := tokenData.Quote[convert]
	if !exists {
		return nil, fmt.Errorf("quote data not found for currency %s", convert)
	}

	// Parse last updated time
	lastUpdated, _ := time.Parse(time.RFC3339, quoteData.LastUpdated)

	// Build result
	result := &TokenPrice{
		ID:                tokenData.ID,
		Name:              tokenData.Name,
		Symbol:            tokenData.Symbol,
		Slug:              tokenData.Slug,
		Price:             quoteData.Price,
		PriceChange24h:    quoteData.PercentChange24h,
		PriceChangePct24h: quoteData.PercentChange24h,
		MarketCap:         quoteData.MarketCap,
		Volume24h:         quoteData.Volume24h,
		LastUpdated:       lastUpdated,
		Quote:             map[string]interface{}{convert: quoteData},
	}

	return result, nil
}

// GetPromptContext provides price context for token transfers to include in LLM prompts
func (t *ERC20PriceLookup) GetPromptContext(ctx context.Context, baggage map[string]interface{}) string {
	// Get token metadata and prices from baggage
	tokenMetadata, hasMetadata := baggage["token_metadata"].(map[string]*TokenMetadata)
	tokenPrices, hasPrices := baggage["token_prices"].(map[string]*TokenPrice)

	if !hasMetadata || !hasPrices || len(tokenPrices) == 0 {
		return ""
	}

	// Build context string with both base prices and transfer values
	var contextParts []string
	for address, price := range tokenPrices {
		if metadata, exists := tokenMetadata[address]; exists {
			// Format base price
			priceStr := fmt.Sprintf("$%.2f", price.Price)
			if price.Price < 0.01 {
				priceStr = fmt.Sprintf("$%.6f", price.Price)
			}

			basePriceInfo := fmt.Sprintf("- %s (%s): %s USD per token", metadata.Name, metadata.Symbol, priceStr)

			// Add transfer values if available
			if len(price.TransferValues) > 0 {
				var transferInfo []string
				for transferID, usdValue := range price.TransferValues {
					tokenAmount := price.TransferAmounts[transferID]
					transferInfo = append(transferInfo, fmt.Sprintf("  • Transfer: %.6f %s = $%.2f USD",
						tokenAmount, metadata.Symbol, usdValue))
				}
				if len(transferInfo) > 0 {
					basePriceInfo += "\n" + strings.Join(transferInfo, "\n")
				}
			}

			contextParts = append(contextParts, basePriceInfo)
		}
	}

	if len(contextParts) == 0 {
		return ""
	}

	return "Token Prices:\n" + strings.Join(contextParts, "\n")
}
