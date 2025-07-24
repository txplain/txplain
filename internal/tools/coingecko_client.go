package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// CoingeckoClient centralizes all Coingecko API interactions using the new On-Chain DEX API
type CoingeckoClient struct {
	apiKey     string
	httpClient *http.Client
	cache      Cache
	verbose    bool
}

// CoingeckoNetwork represents a supported network from Coingecko
type CoingeckoNetwork struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Attributes struct {
		Name                     string `json:"name"`
		ShortName                string `json:"shortname"`
		NativeTokenID            string `json:"native_token_id"`
		ChainIdentifier          int64  `json:"chain_identifier"`
		CoingeckoAssetPlatformID string `json:"coingecko_asset_platform_id"`
		Image                    struct {
			Small string `json:"small"`
			Large string `json:"large"`
			Thumb string `json:"thumb"`
		} `json:"image"`
	} `json:"attributes"`
}

// CoingeckoNetworksResponse represents the response from /onchain/networks
type CoingeckoNetworksResponse struct {
	Data []CoingeckoNetwork `json:"data"`
}

// CoingeckoSimplePriceResponse represents the response from /api/v3/simple/price
type CoingeckoSimplePriceResponse map[string]map[string]float64

// CoingeckoTokenPrice represents token price data from Coingecko
type CoingeckoTokenPrice struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Attributes struct {
		TokenPrices map[string]string `json:"token_prices"`
	} `json:"attributes"`
}

// CoingeckoTokenPriceResponse represents the response from token price API
type CoingeckoTokenPriceResponse struct {
	Data CoingeckoTokenPrice `json:"data"`
}

// CoingeckoTokenPriceResult represents price information for a token from Coingecko
type CoingeckoTokenPriceResult struct {
	Symbol      string    `json:"symbol"`
	Price       float64   `json:"price"`
	Contract    string    `json:"contract_address,omitempty"`
	LastUpdated time.Time `json:"last_updated"`
	PriceSource string    `json:"price_source"`
}

// NewCoingeckoClient creates a new Coingecko client
func NewCoingeckoClient(apiKey string, cache Cache, verbose bool) *CoingeckoClient {
	return &CoingeckoClient{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		cache:   cache,
		verbose: verbose,
	}
}

// IsAvailable returns whether the Coingecko API is available (has API key)
func (c *CoingeckoClient) IsAvailable() bool {
	return c.apiKey != ""
}

// GetNetworkSlug returns the network slug for a given chain ID using environment variables
func (c *CoingeckoClient) GetNetworkSlug(networkID int64) (string, error) {
	return GetNetworkSlugFromEnv(networkID)
}

// GetTokenPrice fetches token price by contract address
func (c *CoingeckoClient) GetTokenPrice(ctx context.Context, networkID int64, contractAddress string) (*CoingeckoTokenPriceResult, error) {
	if !c.IsAvailable() {
		return nil, fmt.Errorf("Coingecko API key not configured")
	}

	// Get network slug
	networkSlug, err := c.GetNetworkSlug(networkID)
	if err != nil {
		return nil, err
	}

	// Check cache first
	if c.cache != nil {
		cacheKey := fmt.Sprintf("coingecko-price:%s:%s", networkSlug, strings.ToLower(contractAddress))
		var cachedPrice CoingeckoTokenPriceResult
		if err := c.cache.GetJSON(ctx, cacheKey, &cachedPrice); err == nil {
			if c.verbose {
				fmt.Printf("  ✅ (cached) Coingecko price for %s: $%.6f\n", contractAddress, cachedPrice.Price)
			}
			return &cachedPrice, nil
		}
	}

	// Fetch from API
	url := fmt.Sprintf("https://api.coingecko.com/api/v3/onchain/simple/networks/%s/token_price/%s",
		networkSlug, strings.ToLower(contractAddress))

	body, err := c.makeRequest(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch token price: %w", err)
	}

	var response CoingeckoTokenPriceResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse price response: %w", err)
	}

	priceStr, exists := response.Data.Attributes.TokenPrices[strings.ToLower(contractAddress)]
	if !exists {
		return nil, fmt.Errorf("price not found for token %s", contractAddress)
	}

	// Parse price from string
	var price float64
	if _, err := fmt.Sscanf(priceStr, "%f", &price); err != nil {
		return nil, fmt.Errorf("failed to parse price %s: %w", priceStr, err)
	}

	tokenPrice := &CoingeckoTokenPriceResult{
		Price:       price,
		Contract:    contractAddress,
		LastUpdated: time.Now(),
		PriceSource: "coingecko-dex",
	}

	// Cache the result (cache for 1 minute as per API docs)
	if c.cache != nil {
		cacheKey := fmt.Sprintf("coingecko-price:%s:%s", networkSlug, strings.ToLower(contractAddress))
		cacheDuration := 1 * time.Minute
		if err := c.cache.SetJSON(ctx, cacheKey, tokenPrice, &cacheDuration); err != nil && c.verbose {
			fmt.Printf("  ⚠️ Failed to cache price for %s: %v\n", contractAddress, err)
		}
	}

	if c.verbose {
		fmt.Printf("  ✅ Coingecko price for %s: $%.6f\n", contractAddress, price)
	}

	return tokenPrice, nil
}

// FetchNetworks fetches and caches network data from Coingecko
func (c *CoingeckoClient) FetchNetworks(ctx context.Context) ([]CoingeckoNetwork, error) {
	if !c.IsAvailable() {
		return nil, fmt.Errorf("Coingecko API key not configured")
	}

	// Check cache first (cache networks for 24 hours)
	if c.cache != nil {
		cacheKey := "coingecko-networks"
		var cachedNetworks []CoingeckoNetwork
		if err := c.cache.GetJSON(ctx, cacheKey, &cachedNetworks); err == nil {
			if c.verbose {
				fmt.Printf("  ✅ (cached) Coingecko networks: %d networks\n", len(cachedNetworks))
			}
			return cachedNetworks, nil
		}
	}

	// Fetch from API
	url := "https://api.coingecko.com/api/v3/onchain/networks"
	body, err := c.makeRequest(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch networks: %w", err)
	}

	var response CoingeckoNetworksResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse networks response: %w", err)
	}

	// Cache the result (cache for 24 hours)
	if c.cache != nil {
		cacheKey := "coingecko-networks"
		cacheDuration := 24 * time.Hour
		if err := c.cache.SetJSON(ctx, cacheKey, response.Data, &cacheDuration); err != nil && c.verbose {
			fmt.Printf("  ⚠️ Failed to cache networks: %v\n", err)
		}
	}

	if c.verbose {
		fmt.Printf("  ✅ Fetched %d networks from Coingecko\n", len(response.Data))
	}

	return response.Data, nil
}

// GetAssetPlatformIDFromSlug returns the Coingecko asset platform ID for a network slug
func (c *CoingeckoClient) GetAssetPlatformIDFromSlug(ctx context.Context, networkSlug string) (string, error) {
	networks, err := c.FetchNetworks(ctx)
	if err != nil {
		return "", err
	}

	for _, network := range networks {
		if network.ID == networkSlug {
			return network.Attributes.CoingeckoAssetPlatformID, nil
		}
	}

	return "", fmt.Errorf("asset platform ID not found for network slug: %s", networkSlug)
}

// GetNativeTokenPriceByAssetID fetches native token price using asset platform ID
func (c *CoingeckoClient) GetNativeTokenPriceByAssetID(ctx context.Context, assetID string) (float64, error) {
	if !c.IsAvailable() {
		return 0, fmt.Errorf("Coingecko API key not configured")
	}

	// Check cache first
	if c.cache != nil {
		cacheKey := fmt.Sprintf("coingecko-native-price-by-id:%s", assetID)
		var cachedPrice float64
		if err := c.cache.GetJSON(ctx, cacheKey, &cachedPrice); err == nil {
			if c.verbose {
				fmt.Printf("  ✅ (cached) Native token price for %s: $%.6f\n", assetID, cachedPrice)
			}
			return cachedPrice, nil
		}
	}

	// Use traditional Coingecko API for native token prices
	url := fmt.Sprintf("https://api.coingecko.com/api/v3/simple/price?ids=%s&vs_currencies=usd", assetID)
	body, err := c.makeRequest(ctx, url)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch native token price: %w", err)
	}

	var priceResponse CoingeckoSimplePriceResponse
	if err := json.Unmarshal(body, &priceResponse); err != nil {
		return 0, fmt.Errorf("failed to parse native token price: %w", err)
	}

	if priceData, exists := priceResponse[assetID]; exists {
		if price, exists := priceData["usd"]; exists {
			// Cache the result
			if c.cache != nil {
				cacheKey := fmt.Sprintf("coingecko-native-price-by-id:%s", assetID)
				cacheDuration := 1 * time.Minute
				if err := c.cache.SetJSON(ctx, cacheKey, price, &cacheDuration); err != nil && c.verbose {
					fmt.Printf("  ⚠️ Failed to cache native price for %s: %v\n", assetID, err)
				}
			}

			if c.verbose {
				fmt.Printf("  ✅ Native token price for %s: $%.6f\n", assetID, price)
			}

			return price, nil
		}
	}

	return 0, fmt.Errorf("price not found for asset ID %s", assetID)
}

// GetNativeTokenPrice fetches native token price by network ID using networks endpoint
func (c *CoingeckoClient) GetNativeTokenPrice(ctx context.Context, networkID int64) (float64, error) {
	if !c.IsAvailable() {
		return 0, fmt.Errorf("Coingecko API key not configured")
	}

	// Get network slug from environment
	networkSlug, err := c.GetNetworkSlug(networkID)
	if err != nil {
		return 0, err
	}

	// Get asset platform ID from network data
	assetID, err := c.GetAssetPlatformIDFromSlug(ctx, networkSlug)
	if err != nil {
		return 0, fmt.Errorf("failed to get asset platform ID for network %d: %w", networkID, err)
	}

	// Fetch price using asset ID
	return c.GetNativeTokenPriceByAssetID(ctx, assetID)
}

// GetNativeTokenSymbol returns the native token symbol for a network using network data
func (c *CoingeckoClient) GetNativeTokenSymbol(networkID int64) string {
	// Try to get from network data first
	ctx := context.Background()
	networks, err := c.FetchNetworks(ctx)
	if err == nil {
		// Get network slug from environment
		networkSlug, err := c.GetNetworkSlug(networkID)
		if err == nil {
			// Find the network and extract symbol from native token ID
			for _, network := range networks {
				if network.ID == networkSlug && network.Attributes.NativeTokenID != "" {
					// Extract symbol from native token ID (e.g., "ethereum" -> "ETH")
					return c.extractSymbolFromAssetID(network.Attributes.NativeTokenID)
				}
			}
		}
	}

	// Fallback to hardcoded mapping for reliability
	symbols := map[int64]string{
		1:     "ETH",   // Ethereum
		137:   "MATIC", // Polygon
		56:    "BNB",   // BSC
		43114: "AVAX",  // Avalanche
		250:   "FTM",   // Fantom
		42161: "ETH",   // Arbitrum
		10:    "ETH",   // Optimism
		25:    "CRO",   // Cronos
		42220: "CELO",  // Celo
		8453:  "ETH",   // Base
	}

	if symbol, exists := symbols[networkID]; exists {
		return symbol
	}

	return ""
}

// extractSymbolFromAssetID extracts a token symbol from Coingecko asset ID
func (c *CoingeckoClient) extractSymbolFromAssetID(assetID string) string {
	// Common mapping from asset IDs to symbols
	assetToSymbol := map[string]string{
		"ethereum":         "ETH",
		"matic-network":    "MATIC",
		"binancecoin":      "BNB",
		"avalanche-2":      "AVAX",
		"fantom":           "FTM",
		"crypto-com-chain": "CRO",
		"celo":             "CELO",
	}

	if symbol, exists := assetToSymbol[assetID]; exists {
		return symbol
	}

	// Default to uppercase version of asset ID
	return strings.ToUpper(assetID)
}

// makeRequest makes an authenticated HTTP request to Coingecko API
func (c *CoingeckoClient) makeRequest(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Use x-cg-demo-api-key for demo plan or x-cg-pro-api-key for pro plan
	req.Header.Set("x-cg-demo-api-key", c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return io.ReadAll(resp.Body)
}
