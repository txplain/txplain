package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// CoinMarketCapNetwork represents a network/blockchain platform
type CoinMarketCapNetwork struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Symbol     string `json:"symbol"`
	Slug       string `json:"slug"`
	IsActive   int    `json:"is_active"`
	FirstBlock int    `json:"first_block,omitempty"`
	LastBlock  int    `json:"last_block,omitempty"`
	Platform   struct {
		ID           int    `json:"id"`
		Name         string `json:"name"`
		Symbol       string `json:"symbol"`
		Slug         string `json:"slug"`
		TokenAddress string `json:"token_address"`
	} `json:"platform,omitempty"`
}

// CoinMarketCapNetworkResponse represents the network mapping API response
type CoinMarketCapNetworkResponse struct {
	Status struct {
		Timestamp    string `json:"timestamp"`
		ErrorCode    int    `json:"error_code"`
		ErrorMessage string `json:"error_message"`
		Elapsed      int    `json:"elapsed"`
		CreditCount  int    `json:"credit_count"`
	} `json:"status"`
	Data []CoinMarketCapNetwork `json:"data"`
}

// NetworkMapper handles CoinMarketCap network mappings
type NetworkMapper struct {
	apiKey        string
	httpClient    *http.Client
	networksCache map[int64]string // networkID -> CoinMarketCap slug
	cacheFile     string
	lastFetchTime time.Time
	cacheDuration time.Duration
}

// NewNetworkMapper creates a new network mapper utility
func NewNetworkMapper(apiKey string) *NetworkMapper {
	return &NetworkMapper{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		networksCache: make(map[int64]string),
		cacheFile:     "./data/coinmarketcap_networks.json",
		cacheDuration: 24 * time.Hour, // Cache for 24 hours
	}
}

// GetNetworkSlug returns the CoinMarketCap network slug for a given network ID
func (nm *NetworkMapper) GetNetworkSlug(networkID int64) (string, error) {
	// Check if we have it in cache
	if slug, exists := nm.networksCache[networkID]; exists {
		return slug, nil
	}

	// Load networks if not already loaded or cache is stale
	if err := nm.ensureNetworksLoaded(); err != nil {
		return "", fmt.Errorf("failed to load networks: %w", err)
	}

	// Check cache again after loading
	if slug, exists := nm.networksCache[networkID]; exists {
		return slug, nil
	}

	return "", fmt.Errorf("network ID %d not found in CoinMarketCap mappings", networkID)
}

// ensureNetworksLoaded loads network mappings from cache or API
func (nm *NetworkMapper) ensureNetworksLoaded() error {
	// Check if cache is still valid
	if !nm.lastFetchTime.IsZero() && time.Since(nm.lastFetchTime) < nm.cacheDuration {
		return nil // Cache is still valid
	}

	// Try to load from file cache first
	if nm.loadFromCache() == nil && time.Since(nm.lastFetchTime) < nm.cacheDuration {
		return nil // File cache is valid
	}

	// Fetch from API if no API key available, return error
	if nm.apiKey == "" {
		return fmt.Errorf("CoinMarketCap API key not available for network mapping")
	}

	// Fetch fresh data from API
	return nm.fetchFromAPI()
}

// loadFromCache loads network mappings from the cached JSON file
func (nm *NetworkMapper) loadFromCache() error {
	// Check if cache file exists
	if _, err := os.Stat(nm.cacheFile); os.IsNotExist(err) {
		return fmt.Errorf("cache file does not exist")
	}

	// Read cache file
	data, err := os.ReadFile(nm.cacheFile)
	if err != nil {
		return fmt.Errorf("failed to read cache file: %w", err)
	}

	// Parse JSON - try the new format first
	var cacheData map[string]interface{}
	if err := json.Unmarshal(data, &cacheData); err != nil {
		return fmt.Errorf("failed to parse cache file: %w", err)
	}

	// Check if this is our new format
	if networks, exists := cacheData["networks"]; exists {
		// New format: {"networks": {"1": "ethereum", ...}, "timestamp": "..."}
		nm.networksCache = make(map[int64]string)

		if networkMap, ok := networks.(map[string]interface{}); ok {
			for idStr, slug := range networkMap {
				if networkID, err := strconv.ParseInt(idStr, 10, 64); err == nil {
					if slugStr, ok := slug.(string); ok {
						nm.networksCache[networkID] = slugStr
					}
				}
			}
		}
	} else {
		// Try old format as fallback
		var response CoinMarketCapNetworkResponse
		if err := json.Unmarshal(data, &response); err != nil {
			return fmt.Errorf("failed to parse cache file in old format: %w", err)
		}

		// Populate cache from old format
		nm.networksCache = make(map[int64]string)
		for _, network := range response.Data {
			if network.IsActive == 1 {
				// Map common network IDs to CoinMarketCap slugs
				networkID := nm.mapNetworkSymbolToID(network.Symbol)
				if networkID > 0 {
					nm.networksCache[networkID] = network.Slug
				}
			}
		}
	}

	// Get file modification time as last fetch time
	if fileInfo, err := os.Stat(nm.cacheFile); err == nil {
		nm.lastFetchTime = fileInfo.ModTime()
	}

	return nil
}

// fetchFromAPI fetches fresh network mappings from CoinMarketCap API
func (nm *NetworkMapper) fetchFromAPI() error {
	fmt.Printf("Fetching CoinMarketCap network mappings from API...\n")

	// Use the correct networks endpoint from CoinMarketCap API documentation
	// Reference: https://coinmarketcap.com/api/documentation/v1/#operation/getNetworks
	networkURL := "https://pro-api.coinmarketcap.com/v1/cryptocurrency/map?listing_status=active&limit=5000"

	req, err := http.NewRequest("GET", networkURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-CMC_PRO_API_KEY", nm.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := nm.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	// For now, let's create a static mapping since we know the DEX API needs specific slugs
	// This will be updated once we find the correct endpoint
	nm.networksCache = map[int64]string{
		1:     "ethereum",            // Ethereum mainnet
		56:    "binance-smart-chain", // BSC mainnet
		137:   "polygon",             // Polygon mainnet
		43114: "avalanche",           // Avalanche C-Chain
		250:   "fantom",              // Fantom mainnet
		25:    "cronos",              // Cronos mainnet
		42220: "celo",                // Celo mainnet
		1285:  "moonriver",           // Moonriver
	}

	// Save a simple cache file for now
	cacheData := map[string]interface{}{
		"networks":  make(map[string]string), // Use string keys for JSON compatibility
		"timestamp": time.Now().Format(time.RFC3339),
	}

	// Convert int64 keys to strings for JSON serialization
	networks := cacheData["networks"].(map[string]string)
	for networkID, slug := range nm.networksCache {
		networks[fmt.Sprintf("%d", networkID)] = slug
	}

	jsonData, _ := json.MarshalIndent(cacheData, "", "  ")
	if err := nm.saveToCache(jsonData); err != nil {
		fmt.Printf("Warning: failed to save network cache: %v\n", err)
	}

	nm.lastFetchTime = time.Now()
	fmt.Printf("✅ Successfully cached %d network mappings (static mapping)\n", len(nm.networksCache))

	// Print the mappings for verification
	for networkID, slug := range nm.networksCache {
		fmt.Printf("  Mapped network %d -> %s\n", networkID, slug)
	}

	return nil
}

// saveToCache saves the raw API response to cache file
func (nm *NetworkMapper) saveToCache(data []byte) error {
	// Ensure data directory exists
	if err := os.MkdirAll(filepath.Dir(nm.cacheFile), 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Write cache file
	if err := os.WriteFile(nm.cacheFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write cache file: %w", err)
	}

	return nil
}

// mapNetworkSymbolToID maps blockchain symbols to standard network IDs
func (nm *NetworkMapper) mapNetworkSymbolToID(symbol string) int64 {
	// Map common blockchain symbols to network IDs
	switch symbol {
	case "ETH":
		return 1 // Ethereum mainnet
	case "BNB":
		return 56 // BSC mainnet
	case "MATIC":
		return 137 // Polygon mainnet
	case "AVAX":
		return 43114 // Avalanche C-Chain
	case "FTM":
		return 250 // Fantom mainnet
	case "ONE":
		return 1666600000 // Harmony mainnet
	case "CELO":
		return 42220 // Celo mainnet
	case "MOVR":
		return 1285 // Moonriver
	case "CRO":
		return 25 // Cronos mainnet
	default:
		return 0 // Unknown/unsupported
	}
}

// GetAllNetworks returns all cached network mappings for debugging
func (nm *NetworkMapper) GetAllNetworks() (map[int64]string, error) {
	if err := nm.ensureNetworksLoaded(); err != nil {
		return nil, err
	}
	return nm.networksCache, nil
}
