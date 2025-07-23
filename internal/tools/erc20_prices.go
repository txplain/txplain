package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/txplain/txplain/internal/models"
)

// ERC20PriceLookup fetches token prices from CoinMarketCap API
type ERC20PriceLookup struct {
	apiKey        string
	httpClient    *http.Client
	networkMapper *NetworkMapper
	verbose       bool
	cache         Cache // Cache for price data
}

// DEXPriceData represents DEX-specific pricing information
type DEXPriceData struct {
	PlatformID        int     `json:"platform_id"`
	PlatformName      string  `json:"platform_name"`
	PlatformSlug      string  `json:"platform_slug"`
	DexID             int     `json:"dex_id"`
	DexName           string  `json:"dex_name"`
	DexSlug           string  `json:"dex_slug"`
	PairAddress       string  `json:"pair_address"`
	BaseSymbol        string  `json:"base_symbol"`
	QuoteSymbol       string  `json:"quote_symbol"`
	Category          string  `json:"category"`
	FeeType           string  `json:"fee_type"`
	LiquidityUSD      float64 `json:"liquidity_usd"`
	VolumeUSD24h      float64 `json:"volume_usd_24h"`
	VolumeChange24h   float64 `json:"volume_change_24h"`
	Price             float64 `json:"price"`
	PriceBase         float64 `json:"price_base"`
	PriceQuote        float64 `json:"price_quote"`
	PriceChange24h    float64 `json:"price_change_24h"`
	PriceChangePct24h float64 `json:"price_change_pct_24h"`
	LastUpdated       string  `json:"last_updated"`
	NumMarketPairs    int     `json:"num_market_pairs"`
	MarketPairID      int     `json:"market_pair_id"`
	MarketPairBaseID  int     `json:"market_pair_base_id"`
	MarketPairQuoteID int     `json:"market_pair_quote_id"`
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
	// New DEX-specific fields
	DEXData      []DEXPriceData `json:"dex_data,omitempty"`          // DEX pricing from multiple sources
	DEXPrice     float64        `json:"dex_price,omitempty"`         // Best DEX price (weighted by liquidity)
	DEXLiquidity float64        `json:"dex_liquidity_usd,omitempty"` // Total DEX liquidity
	DEXVolume24h float64        `json:"dex_volume_24h,omitempty"`    // Total DEX volume
	HasDEXData   bool           `json:"has_dex_data,omitempty"`      // Whether DEX data was found
	PriceSource  string         `json:"price_source,omitempty"`      // "CEX", "DEX", or "COMBINED"
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

// CoinMarketCapDEXQuoteResponse represents the response from /v4/dex/pairs/quotes/latest
type CoinMarketCapDEXQuoteResponse struct {
	Status struct {
		Timestamp    string `json:"timestamp"`
		ErrorCode    int    `json:"error_code"`
		ErrorMessage string `json:"error_message"`
		Elapsed      int    `json:"elapsed"`
		CreditCount  int    `json:"credit_count"`
	} `json:"status"`
	Data []struct {
		PlatformID        int     `json:"platform_id"`
		PlatformName      string  `json:"platform_name"`
		PlatformSlug      string  `json:"platform_slug"`
		DexID             int     `json:"dex_id"`
		DexName           string  `json:"dex_name"`
		DexSlug           string  `json:"dex_slug"`
		PairAddress       string  `json:"pair_address"`
		BaseSymbol        string  `json:"base_symbol"`
		QuoteSymbol       string  `json:"quote_symbol"`
		Category          string  `json:"category"`
		FeeType           string  `json:"fee_type"`
		LiquidityUSD      float64 `json:"liquidity_usd"`
		VolumeUSD24h      float64 `json:"volume_usd_24h"`
		VolumeChange24h   float64 `json:"volume_change_24h"`
		Price             float64 `json:"price"`
		PriceBase         float64 `json:"price_base"`
		PriceQuote        float64 `json:"price_quote"`
		PriceChange24h    float64 `json:"price_change_24h"`
		PriceChangePct24h float64 `json:"price_change_pct_24h"`
		LastUpdated       string  `json:"last_updated"`
		NumMarketPairs    int     `json:"num_market_pairs"`
		MarketPairID      int     `json:"market_pair_id"`
		MarketPairBaseID  int     `json:"market_pair_base_id"`
		MarketPairQuoteID int     `json:"market_pair_quote_id"`
	} `json:"data"`
}

// NewERC20PriceLookup creates a new ERC20 price lookup tool
func NewERC20PriceLookup(apiKey string, cache Cache, verbose bool) *ERC20PriceLookup {
	networkMapper := NewNetworkMapper(apiKey, cache)
	return &ERC20PriceLookup{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 300 * time.Second, // 5 minutes for price API calls
		},
		networkMapper: networkMapper,
		verbose:       verbose,
		cache:         cache,
	}
}

// Name returns the tool name
func (t *ERC20PriceLookup) Name() string {
	return "erc20_price_lookup"
}

// Description returns the tool description
func (t *ERC20PriceLookup) Description() string {
	return "Looks up ERC20 token prices using CoinMarketCap API with both centralized exchange (CEX) and decentralized exchange (DEX) data. Supports lookup by symbol, name, or contract address."
}

// Dependencies returns the tools this processor depends on
func (t *ERC20PriceLookup) Dependencies() []string {
	return []string{"token_metadata_enricher", "amounts_finder"} // Needs token metadata for optimal lookups and amounts for native token pricing
}

// Process adds token price information to baggage
func (t *ERC20PriceLookup) Process(ctx context.Context, baggage map[string]interface{}) error {
	if t.verbose {
		fmt.Println("\n" + strings.Repeat("💰", 60))
		fmt.Println("🔍 ERC20 PRICE LOOKUP: Starting token price fetching")
		fmt.Printf("🔑 CoinMarketCap API Key available: %t\n", t.apiKey != "")
		fmt.Println(strings.Repeat("💰", 60))
	}

	if t.apiKey == "" {
		if t.verbose {
			fmt.Println("⚠️  No CoinMarketCap API key provided, skipping price lookup")
			fmt.Println(strings.Repeat("💰", 60) + "\n")
		}
		return nil // No API key, skip price lookup
	}

	// Get network ID from baggage for DEX pricing
	networkID := int64(0) // No default - require explicit network
	if rawData, ok := baggage["raw_data"].(map[string]interface{}); ok {
		if nid, ok := rawData["network_id"].(float64); ok {
			networkID = int64(nid)
		}
	}

	// Skip if no network ID provided
	if networkID == 0 {
		if t.verbose {
			fmt.Println("⚠️  No network ID provided, skipping price lookup")
			fmt.Println(strings.Repeat("💰", 60) + "\n")
		}
		return nil
	}

	if t.verbose {
		fmt.Printf("🌐 Network ID: %d\n", networkID)
	}

	// Check if we need native token price for gas fees BEFORE checking token metadata
	needsNativeTokenPrice := t.checkForNativeTokenNeeds(baggage, networkID)

	// Get token metadata from baggage
	tokenMetadata, ok := baggage["token_metadata"].(map[string]*TokenMetadata)
	if (!ok || len(tokenMetadata) == 0) && !needsNativeTokenPrice {
		if t.verbose {
			fmt.Println("⚠️  No token metadata found and no native token price needed, skipping price lookup")
			fmt.Println(strings.Repeat("💰", 60) + "\n")
		}
		return nil // No token metadata and no native token needed, nothing to price
	}

	// Get progress tracker from baggage if available
	progressTracker, hasProgress := baggage["progress_tracker"].(*models.ProgressTracker)

	if t.verbose {
		if tokenMetadata != nil && len(tokenMetadata) > 0 {
			fmt.Printf("📊 Found %d tokens to price\n", len(tokenMetadata))

			// Show tokens to be priced
			erc20Count := 0
			for _, metadata := range tokenMetadata {
				if metadata.Type == "ERC20" || (metadata.Type == "Contract" && metadata.Decimals > 0) {
					erc20Count++
				}
			}
			fmt.Printf("💹 %d tokens eligible for pricing\n", erc20Count)
		} else {
			fmt.Printf("📊 No ERC20 tokens found, but native token price needed\n")
		}
		fmt.Println("🔄 Looking up token prices...")
	}

	// Look up prices for each token
	tokenPrices := make(map[string]*TokenPrice)
	successCount := 0
	totalToProcess := 0

	// Count total tokens to process for progress updates
	if needsNativeTokenPrice {
		totalToProcess++
	}
	if tokenMetadata != nil {
		for _, metadata := range tokenMetadata {
			if metadata.Type == "ERC20" || (metadata.Type == "Contract" && metadata.Decimals > 0) {
				totalToProcess++
			}
		}
	}

	currentIndex := 0

	// First, check if we need to fetch native token price for gas fees
	if needsNativeTokenPrice {
		currentIndex++
		nativeSymbol := t.networkMapper.GetNativeTokenSymbol(networkID)
		if nativeSymbol != "" {
			// Send progress update for native token pricing
			if hasProgress {
				progress := fmt.Sprintf("Fetching native token price (%d/%d): %s", currentIndex, totalToProcess, nativeSymbol)
				progressTracker.UpdateComponent("erc20_price_lookup", models.ComponentGroupEnrichment, "Fetching Token Prices", models.ComponentStatusRunning, progress)
			}

			if t.verbose {
				fmt.Printf("   Native %s (gas fees)...", nativeSymbol)
			}

			// Look up native token price by symbol
			priceInput := map[string]interface{}{
				"symbol":     nativeSymbol,
				"network_id": networkID,
			}

			if price, err := t.lookupTokenPrice(ctx, priceInput); err == nil && price != nil {
				// Store native token price using "native" as key for easy lookup
				tokenPrices["native"] = &TokenPrice{
					Symbol:      nativeSymbol,
					Price:       price.Price,
					PriceSource: price.PriceSource,
				}
				successCount++

				if t.verbose {
					fmt.Printf(" ✅ $%.6f\n", price.Price)
				}
			} else if t.verbose {
				fmt.Printf(" ❌ Failed: %v\n", err)
			}
		}
	}

	// Then process regular ERC20 tokens
	if tokenMetadata != nil {
		for address, metadata := range tokenMetadata {
			if metadata.Type == "ERC20" {
				currentIndex++

				// Send progress update for each ERC20 token
				if hasProgress {
					progress := fmt.Sprintf("Fetching ERC20 price (%d/%d): %s", currentIndex, totalToProcess, metadata.Symbol)
					progressTracker.UpdateComponent("erc20_price_lookup", models.ComponentGroupEnrichment, "Fetching Token Prices", models.ComponentStatusRunning, progress)
				}

				if t.verbose {
					fmt.Printf("   ERC20 %s (%s)...", metadata.Symbol, address[:10]+"...")
				}

				priceInput := map[string]interface{}{
					"contract_address": address,
					"network_id":       float64(networkID), // Convert to float64 to match Run method expectation
				}
				if metadata.Symbol != "" {
					priceInput["symbol"] = metadata.Symbol
				}

				result, err := t.Run(ctx, priceInput)
				if err == nil {
					if tokenData, ok := result["token"].(*TokenPrice); ok {
						tokenData.Contract = address // Ensure contract address is set
						tokenPrices[address] = tokenData
						successCount++

						if t.verbose {
							fmt.Printf(" ✅ $%.6f\n", tokenData.Price)
						}
					} else if t.verbose {
						fmt.Printf(" ❌ Invalid response format\n")
					}
				} else if t.verbose {
					fmt.Printf(" ❌ %v\n", err)
				}
			} else if metadata.Type == "Contract" && metadata.Decimals > 0 {
				currentIndex++

				// Send progress update for contract tokens
				if hasProgress {
					progress := fmt.Sprintf("Fetching contract price (%d/%d): %s", currentIndex, totalToProcess, address[:10]+"...")
					progressTracker.UpdateComponent("erc20_price_lookup", models.ComponentGroupEnrichment, "Fetching Token Prices", models.ComponentStatusRunning, progress)
				}

				if t.verbose {
					fmt.Printf("   Contract %s (has decimals)...", address[:10]+"...")
				}

				// Try DEX pricing for contracts that might be tokens (have decimals but no name/symbol)
				priceInput := map[string]interface{}{
					"contract_address": address,
					"network_id":       float64(networkID), // Convert to float64 to match Run method expectation
				}

				result, err := t.Run(ctx, priceInput)
				if err == nil {
					if tokenData, ok := result["token"].(*TokenPrice); ok {
						tokenData.Contract = address // Ensure contract address is set
						tokenPrices[address] = tokenData
						successCount++

						if t.verbose {
							fmt.Printf(" ✅ $%.6f (DEX)\n", tokenData.Price)
						}
					} else if t.verbose {
						fmt.Printf(" ❌ Invalid response format\n")
					}
				} else if t.verbose {
					fmt.Printf(" ❌ %v\n", err)
				}
			}
		}
	} // End of tokenMetadata != nil check

	// Send final progress update with results summary
	if hasProgress {
		if successCount > 0 {
			progress := fmt.Sprintf("Completed: Found prices for %d out of %d tokens", successCount, totalToProcess)
			progressTracker.UpdateComponent("erc20_price_lookup", models.ComponentGroupEnrichment, "Fetching Token Prices", models.ComponentStatusFinished, progress)
		} else {
			progress := fmt.Sprintf("Completed: No prices found for %d tokens", totalToProcess)
			progressTracker.UpdateComponent("erc20_price_lookup", models.ComponentGroupEnrichment, "Fetching Token Prices", models.ComponentStatusFinished, progress)
		}
	}

	if t.verbose {
		fmt.Printf("✅ Successfully fetched prices for %d/%d eligible tokens\n", successCount, len(tokenMetadata))
	}

	// Calculate USD values for transfers if available
	t.calculateTransferValues(baggage, tokenPrices, tokenMetadata)

	if t.verbose {
		fmt.Println("\n" + strings.Repeat("💰", 60))
		fmt.Println("✅ ERC20 PRICE LOOKUP: Completed successfully")
		fmt.Println(strings.Repeat("💰", 60) + "\n")
	}

	// Add token prices to baggage
	baggage["token_prices"] = tokenPrices
	return nil
}

// checkForNativeTokenNeeds determines if native token price needs to be fetched for gas fees
func (t *ERC20PriceLookup) checkForNativeTokenNeeds(baggage map[string]interface{}, networkID int64) bool {
	// Check if AmountsFinder detected any native token amounts (like gas fees)
	detectedAmounts, ok := baggage["detected_amounts"].([]DetectedAmount)
	if !ok {
		return false
	}

	// Look for any detected amounts with token_contract="native"
	for _, amount := range detectedAmounts {
		if amount.TokenContract == "native" {
			return true // Found native token usage, need to fetch price
		}
	}

	return false
}

// lookupTokenPrice is a helper to fetch a single token price using the Run method
func (t *ERC20PriceLookup) lookupTokenPrice(ctx context.Context, input map[string]interface{}) (*TokenPrice, error) {
	// Extract search parameters from input
	symbol, _ := input["symbol"].(string)
	name, _ := input["name"].(string)
	contractAddress, _ := input["contract_address"].(string)
	convert, _ := input["convert"].(string) // Currency to convert to (default: USD)
	networkIDFloat, _ := input["network_id"].(float64)
	networkID := int64(networkIDFloat)
	if networkID == 0 {
		return nil, NewToolError("erc20_price_lookup", "network_id is required", "MISSING_NETWORK")
	}

	if convert == "" {
		convert = "USD"
	}

	// Need at least one search parameter
	if symbol == "" && name == "" && contractAddress == "" {
		return nil, NewToolError("erc20_price_lookup", "must provide symbol, name, or contract_address", "MISSING_PARAMS")
	}

	var tokenPrice *TokenPrice

	// Step 1: Try to get CEX price data (traditional CoinMarketCap API)
	cexPrice, cexErr := t.getCEXPrice(ctx, symbol, name, contractAddress, convert)

	// Step 2: Try to get DEX price data (new DEX API)
	var dexData []DEXPriceData
	var dexErr error
	if contractAddress != "" && networkID != 0 {
		dexData, dexErr = t.getDEXPrice(ctx, contractAddress, networkID)
		// DEX API failure is not critical - we can still use CEX data
		if dexErr != nil && cexErr == nil {
			// If CEX succeeded but DEX failed, continue with CEX data only
			dexErr = nil // Clear DEX error to avoid double failure
		}
	}

	// Step 3: Combine the data intelligently
	if cexErr != nil && dexErr != nil {
		// Both failed
		return nil, NewToolError("erc20_price_lookup", fmt.Sprintf("failed to get price from both CEX (%v) and DEX (%v)", cexErr, dexErr), "PRICE_FETCH_ERROR")
	}

	if cexErr == nil {
		// CEX data available
		tokenPrice = cexPrice
		tokenPrice.PriceSource = "CEX"
	} else {
		// Only DEX data available, create TokenPrice from DEX data
		tokenPrice = &TokenPrice{
			Symbol:      symbol,
			Contract:    contractAddress,
			PriceSource: "DEX",
			HasDEXData:  len(dexData) > 0,
		}
		if len(dexData) > 0 {
			// Use the best DEX price (highest liquidity)
			tokenPrice.DEXPrice = dexData[0].Price
			tokenPrice.Price = tokenPrice.DEXPrice
		}
	}

	// Step 4: Enhance with DEX data if available
	if dexErr == nil && len(dexData) > 0 {
		tokenPrice.DEXData = dexData
		tokenPrice.HasDEXData = true

		// Calculate DEX metrics
		var totalLiquidity, totalVolume float64
		var liquidityWeightedPrice float64

		for _, dex := range dexData {
			totalLiquidity += dex.LiquidityUSD
			totalVolume += dex.VolumeUSD24h

			// Weighted average price by liquidity
			if dex.LiquidityUSD > 0 {
				liquidityWeightedPrice += dex.Price * dex.LiquidityUSD
			}
		}

		tokenPrice.DEXLiquidity = totalLiquidity
		tokenPrice.DEXVolume24h = totalVolume

		if totalLiquidity > 0 {
			tokenPrice.DEXPrice = liquidityWeightedPrice / totalLiquidity
		}

		// If we have both CEX and DEX data, mark as combined
		if cexErr == nil {
			tokenPrice.PriceSource = "COMBINED"

			// Use DEX price if CEX price is stale or DEX has significant liquidity
			if totalLiquidity > 100000 { // $100k+ liquidity threshold
				// Blend prices: favor CEX for major tokens, DEX for newer tokens
				blendRatio := 0.3 // 30% DEX, 70% CEX for established tokens
				tokenPrice.Price = (tokenPrice.Price * (1 - blendRatio)) + (tokenPrice.DEXPrice * blendRatio)
			}
		}
	}

	return tokenPrice, nil
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

// Run executes the price lookup with both CEX and DEX data
func (t *ERC20PriceLookup) Run(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
	// Extract search parameters from input
	symbol, _ := input["symbol"].(string)
	name, _ := input["name"].(string)
	contractAddress, _ := input["contract_address"].(string)
	convert, _ := input["convert"].(string) // Currency to convert to (default: USD)
	networkIDFloat, _ := input["network_id"].(float64)
	networkID := int64(networkIDFloat)
	if networkID == 0 {
		return nil, NewToolError("erc20_price_lookup", "network_id is required", "MISSING_NETWORK")
	}

	if convert == "" {
		convert = "USD"
	}

	// Need at least one search parameter
	if symbol == "" && name == "" && contractAddress == "" {
		return nil, NewToolError("erc20_price_lookup", "must provide symbol, name, or contract_address", "MISSING_PARAMS")
	}

	var tokenPrice *TokenPrice

	// Step 1: Try to get CEX price data (traditional CoinMarketCap API)
	cexPrice, cexErr := t.getCEXPrice(ctx, symbol, name, contractAddress, convert)

	// Step 2: Try to get DEX price data (new DEX API)
	var dexData []DEXPriceData
	var dexErr error
	if contractAddress != "" && networkID != 0 {
		dexData, dexErr = t.getDEXPrice(ctx, contractAddress, networkID)
		// DEX API failure is not critical - we can still use CEX data
		if dexErr != nil && cexErr == nil {
			// If CEX succeeded but DEX failed, continue with CEX data only
			dexErr = nil // Clear DEX error to avoid double failure
		}
	}

	// Step 3: Combine the data intelligently
	if cexErr != nil && dexErr != nil {
		// Both failed
		return nil, NewToolError("erc20_price_lookup", fmt.Sprintf("failed to get price from both CEX (%v) and DEX (%v)", cexErr, dexErr), "PRICE_FETCH_ERROR")
	}

	if cexErr == nil {
		// CEX data available
		tokenPrice = cexPrice
		tokenPrice.PriceSource = "CEX"
	} else {
		// Only DEX data available, create TokenPrice from DEX data
		tokenPrice = &TokenPrice{
			Symbol:      symbol,
			Contract:    contractAddress,
			PriceSource: "DEX",
			HasDEXData:  len(dexData) > 0,
		}
		if len(dexData) > 0 {
			// Use the best DEX price (highest liquidity)
			tokenPrice.DEXPrice = dexData[0].Price
			tokenPrice.Price = tokenPrice.DEXPrice
		}
	}

	// Step 4: Enhance with DEX data if available
	if dexErr == nil && len(dexData) > 0 {
		tokenPrice.DEXData = dexData
		tokenPrice.HasDEXData = true

		// Calculate DEX metrics
		var totalLiquidity, totalVolume float64
		var liquidityWeightedPrice float64

		for _, dex := range dexData {
			totalLiquidity += dex.LiquidityUSD
			totalVolume += dex.VolumeUSD24h

			// Weighted average price by liquidity
			if dex.LiquidityUSD > 0 {
				liquidityWeightedPrice += dex.Price * dex.LiquidityUSD
			}
		}

		tokenPrice.DEXLiquidity = totalLiquidity
		tokenPrice.DEXVolume24h = totalVolume

		if totalLiquidity > 0 {
			tokenPrice.DEXPrice = liquidityWeightedPrice / totalLiquidity
		}

		// If we have both CEX and DEX data, mark as combined
		if cexErr == nil {
			tokenPrice.PriceSource = "COMBINED"

			// Use DEX price if CEX price is stale or DEX has significant liquidity
			if totalLiquidity > 100000 { // $100k+ liquidity threshold
				// Blend prices: favor CEX for major tokens, DEX for newer tokens
				blendRatio := 0.3 // 30% DEX, 70% CEX for established tokens
				tokenPrice.Price = (tokenPrice.Price * (1 - blendRatio)) + (tokenPrice.DEXPrice * blendRatio)
			}
		}
	}

	return map[string]interface{}{
		"token": tokenPrice,
	}, nil
}

// getCEXPrice fetches price from traditional CoinMarketCap API
func (t *ERC20PriceLookup) getCEXPrice(ctx context.Context, symbol, name, contractAddress, convert string) (*TokenPrice, error) {
	// Step 1: Find the token using the map endpoint
	tokenID, err := t.findToken(ctx, symbol, name, contractAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to find token: %w", err)
	}

	// Step 2: Get price information
	priceInfo, err := t.getTokenPrice(ctx, tokenID, convert)
	if err != nil {
		return nil, fmt.Errorf("failed to get price: %w", err)
	}

	return priceInfo, nil
}

// getDEXPrice fetches price data from CoinMarketCap DEX API
func (t *ERC20PriceLookup) getDEXPrice(ctx context.Context, contractAddress string, networkID int64) ([]DEXPriceData, error) {
	if t.apiKey == "" {
		return nil, fmt.Errorf("CoinMarketCap API key not configured")
	}

	// Get blockchain slug for DEX API
	blockchainSlug, err := t.networkMapper.GetNetworkSlug(networkID)
	if err != nil {
		return nil, fmt.Errorf("failed to get network slug for DEX API: %w", err)
	}

	// Build query parameters for DEX API
	params := url.Values{}
	params.Set("network_slug", blockchainSlug) // Use network_slug instead of blockchain
	params.Set("token_address", strings.ToLower(contractAddress))
	params.Set("convert", "USD")
	params.Set("sort", "liquidity_usd") // Sort by liquidity for best prices first
	params.Set("limit", "10")           // Get top 10 DEX pairs

	// Construct URL for DEX pairs quotes
	dexURL := fmt.Sprintf("https://pro-api.coinmarketcap.com/v4/dex/pairs/quotes/latest?%s", params.Encode())

	// Make request
	req, err := http.NewRequestWithContext(ctx, "GET", dexURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create DEX request: %w", err)
	}

	req.Header.Set("X-CMC_PRO_API_KEY", t.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make DEX request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("DEX API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read DEX response: %w", err)
	}

	var dexResp CoinMarketCapDEXQuoteResponse
	if err := json.Unmarshal(body, &dexResp); err != nil {
		return nil, fmt.Errorf("failed to parse DEX response: %w", err)
	}

	if dexResp.Status.ErrorCode != 0 {
		return nil, fmt.Errorf("DEX API error: %s", dexResp.Status.ErrorMessage)
	}

	// Convert response data to our format
	var dexData []DEXPriceData
	for _, pair := range dexResp.Data {
		dexPrice := DEXPriceData{
			PlatformID:        pair.PlatformID,
			PlatformName:      pair.PlatformName,
			PlatformSlug:      pair.PlatformSlug,
			DexID:             pair.DexID,
			DexName:           pair.DexName,
			DexSlug:           pair.DexSlug,
			PairAddress:       pair.PairAddress,
			BaseSymbol:        pair.BaseSymbol,
			QuoteSymbol:       pair.QuoteSymbol,
			Category:          pair.Category,
			FeeType:           pair.FeeType,
			LiquidityUSD:      pair.LiquidityUSD,
			VolumeUSD24h:      pair.VolumeUSD24h,
			VolumeChange24h:   pair.VolumeChange24h,
			Price:             pair.Price,
			PriceBase:         pair.PriceBase,
			PriceQuote:        pair.PriceQuote,
			PriceChange24h:    pair.PriceChange24h,
			PriceChangePct24h: pair.PriceChangePct24h,
			LastUpdated:       pair.LastUpdated,
			NumMarketPairs:    pair.NumMarketPairs,
			MarketPairID:      pair.MarketPairID,
			MarketPairBaseID:  pair.MarketPairBaseID,
			MarketPairQuoteID: pair.MarketPairQuoteID,
		}
		dexData = append(dexData, dexPrice)
	}

	return dexData, nil
}

// findToken searches for a token using CoinMarketCap's map endpoint
func (t *ERC20PriceLookup) findToken(ctx context.Context, symbol, name, contractAddress string) (int, error) {
	if t.apiKey == "" {
		return 0, fmt.Errorf("CoinMarketCap API key not configured")
	}

	// Check cache first if available
	if t.cache != nil {
		// Create cache key based on search parameters (lowercase address for consistency)
		cacheKeyParams := fmt.Sprintf("%s:%s:%s", symbol, name, strings.ToLower(contractAddress))
		cacheKey := fmt.Sprintf(CMCMappingKeyPattern, 0, cacheKeyParams) // Use 0 for network-agnostic mapping

		if t.verbose {
			fmt.Printf("  Checking cache for token mapping with key: %s\n", cacheKey)
		}

		var cachedTokenID int
		if err := t.cache.GetJSON(ctx, cacheKey, &cachedTokenID); err == nil {
			if t.verbose {
				fmt.Printf("  ✅ Found cached token mapping: %s -> ID %d\n", cacheKeyParams, cachedTokenID)
			}
			return cachedTokenID, nil
		} else if t.verbose {
			fmt.Printf("  Cache miss for token mapping %s: %v\n", cacheKeyParams, err)
		}
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

	tokenID := bestMatch.ID

	// Cache successful result if cache is available
	if t.cache != nil {
		cacheKeyParams := fmt.Sprintf("%s:%s:%s", symbol, name, strings.ToLower(contractAddress))
		cacheKey := fmt.Sprintf(CMCMappingKeyPattern, 0, cacheKeyParams)

		// Cache token mapping with metadata TTL (60 days)
		if err := t.cache.SetJSON(ctx, cacheKey, tokenID, &MetadataTTLDuration); err != nil {
			if t.verbose {
				fmt.Printf("  ⚠️  Failed to cache token mapping %s: %v\n", cacheKeyParams, err)
			}
		} else if t.verbose {
			fmt.Printf("  ✅ Cached token mapping: %s -> ID %d\n", cacheKeyParams, tokenID)
		}
	}

	return tokenID, nil
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
	// Check cache first if available
	if t.cache != nil {
		cacheKey := fmt.Sprintf(TokenPriceKeyPattern, 0, fmt.Sprintf("id:%d:%s", tokenID, convert))

		if t.verbose {
			fmt.Printf("  Checking cache for token price with key: %s\n", cacheKey)
		}

		var cachedPrice TokenPrice
		if err := t.cache.GetJSON(ctx, cacheKey, &cachedPrice); err == nil {
			if t.verbose {
				fmt.Printf("  ✅ Found cached token price: ID %d -> $%.6f\n", tokenID, cachedPrice.Price)
			}
			return &cachedPrice, nil
		} else if t.verbose {
			fmt.Printf("  Cache miss for token price ID %d: %v\n", tokenID, err)
		}
	}

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

	// Cache successful result if cache is available - use 1 hour TTL for prices
	if t.cache != nil {
		cacheKey := fmt.Sprintf(TokenPriceKeyPattern, 0, fmt.Sprintf("id:%d:%s", tokenID, convert))

		if err := t.cache.SetJSON(ctx, cacheKey, result, &PriceTTLDuration); err != nil {
			if t.verbose {
				fmt.Printf("  ⚠️  Failed to cache token price ID %d: %v\n", tokenID, err)
			}
		} else if t.verbose {
			fmt.Printf("  ✅ Cached token price: ID %d -> $%.6f\n", tokenID, result.Price)
		}
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

			// Add price source information
			if price.PriceSource != "" {
				basePriceInfo += fmt.Sprintf(" [%s]", price.PriceSource)
			}

			// Add DEX information if available
			if price.HasDEXData && len(price.DEXData) > 0 {
				basePriceInfo += fmt.Sprintf("\n  • DEX Price: $%.6f", price.DEXPrice)
				basePriceInfo += fmt.Sprintf(" (Liquidity: $%.0f)", price.DEXLiquidity)
				basePriceInfo += fmt.Sprintf(" from %d DEX pairs", len(price.DEXData))

				// Show top DEX sources
				if len(price.DEXData) > 0 {
					topDex := price.DEXData[0] // Already sorted by liquidity
					basePriceInfo += fmt.Sprintf("\n  • Top DEX: %s ($%.0f liquidity)",
						topDex.DexName, topDex.LiquidityUSD)
				}
			}

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

	return "Token Prices (CEX + DEX Data):\n" + strings.Join(contextParts, "\n")
}

// GetRagContext provides RAG context for price information (minimal for this tool)
func (t *ERC20PriceLookup) GetRagContext(ctx context.Context, baggage map[string]interface{}) *RagContext {
	ragContext := NewRagContext()
	// Price lookup provides current market data, not historical knowledge for RAG
	return ragContext
}
