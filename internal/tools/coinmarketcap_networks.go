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
	apiKey               string
	httpClient           *http.Client
	networksCache        map[int64]string                // networkID -> CoinMarketCap slug
	nativeTokenCache     map[int64]string                // networkID -> native token symbol
	networkPlatformCache map[string]CoinMarketCapNetwork // slug -> network data
	cacheFile            string
	lastFetchTime        time.Time
	cacheDuration        time.Duration
	cache                Cache // Cache for network mappings
}

// NewNetworkMapper creates a new network mapper utility
func NewNetworkMapper(apiKey string) *NetworkMapper {
	return &NetworkMapper{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 300 * time.Second, // 5 minutes for CoinMarketCap API calls
		},
		networksCache:        make(map[int64]string),
		nativeTokenCache:     make(map[int64]string),
		networkPlatformCache: make(map[string]CoinMarketCapNetwork),
		cacheFile:            "./data/coinmarketcap_networks.json",
		cacheDuration:        24 * time.Hour, // Cache for 24 hours
		cache:                nil,            // Set via SetCache
	}
}

// SetCache sets the cache instance for the network mapper
func (nm *NetworkMapper) SetCache(cache Cache) {
	nm.cache = cache
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
		// Try old format as fallback - but don't process it since we removed hardcoded mappings
		// Just log that we found old format data
		var response CoinMarketCapNetworkResponse
		if err := json.Unmarshal(data, &response); err != nil {
			return fmt.Errorf("failed to parse cache file in any known format: %w", err)
		}

		// Old format detected but we can't process it without hardcoded mappings
		return fmt.Errorf("old cache format detected but not supported - please refresh cache")
	}

	// Get file modification time as last fetch time
	if fileInfo, err := os.Stat(nm.cacheFile); err == nil {
		nm.lastFetchTime = fileInfo.ModTime()
	}

	return nil
}

// fetchFromAPI fetches fresh network mappings from CoinMarketCap API
func (nm *NetworkMapper) fetchFromAPI() error {
	fmt.Printf("Loading CoinMarketCap network mappings from cache/fallback...\n")

	// For now, load from our cached data file since we have the network mappings there
	// This avoids hardcoding while using the existing cached mappings
	if err := nm.loadFromCache(); err == nil {
		fmt.Printf("✅ Successfully loaded %d network mappings from cache\n", len(nm.networksCache))
		return nil
	}

	// Fallback: if no API endpoint is available yet, return error
	return fmt.Errorf("network mapping data not available - please ensure coinmarketcap_networks.json cache file exists")
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

// GetAllNetworks returns all cached network mappings for debugging
func (nm *NetworkMapper) GetAllNetworks() (map[int64]string, error) {
	if err := nm.ensureNetworksLoaded(); err != nil {
		return nil, err
	}
	return nm.networksCache, nil
}

// GetNativeTokenSymbol returns the native token symbol for a given network ID
func (nm *NetworkMapper) GetNativeTokenSymbol(networkID int64) string {
	// Check cache first
	if symbol, exists := nm.nativeTokenCache[networkID]; exists {
		return symbol
	}

	// Simple fallback mapping for common networks
	fallbackSymbols := map[int64]string{
		1:     "ETH",   // Ethereum
		137:   "MATIC", // Polygon
		56:    "BNB",   // BSC
		43114: "AVAX",  // Avalanche
		250:   "FTM",   // Fantom
		42161: "ETH",   // Arbitrum
		10:    "ETH",   // Optimism
		25:    "CRO",   // Cronos
		42220: "CELO",  // Celo
		1285:  "MOVR",  // Moonriver
	}

	// Check fallback mapping first
	if symbol, exists := fallbackSymbols[networkID]; exists {
		// Cache the result
		nm.nativeTokenCache[networkID] = symbol
		return symbol
	}

	// Get network slug
	slug, err := nm.GetNetworkSlug(networkID)
	if err != nil {
		return "" // Network not found
	}

	// Try to get platform data for this network
	if err := nm.ensurePlatformDataLoaded(); err != nil {
		return "" // Failed to load platform data
	}

	// Look up platform data
	if platformData, exists := nm.networkPlatformCache[slug]; exists {
		if platformData.Symbol != "" {
			// Cache the result
			nm.nativeTokenCache[networkID] = platformData.Symbol
			return platformData.Symbol
		}
	}

	return "" // No native token symbol found
}

// ensurePlatformDataLoaded loads platform/network data from CoinMarketCap API
func (nm *NetworkMapper) ensurePlatformDataLoaded() error {
	// Check if we already have platform data and it's not stale
	if len(nm.networkPlatformCache) > 0 && !nm.lastFetchTime.IsZero() && time.Since(nm.lastFetchTime) < nm.cacheDuration {
		return nil
	}

	if nm.apiKey == "" {
		return fmt.Errorf("CoinMarketCap API key required for fetching platform data")
	}

	// Fetch platform data from CoinMarketCap API
	return nm.fetchPlatformDataFromAPI()
}

// fetchPlatformDataFromAPI fetches platform/network data from CoinMarketCap
func (nm *NetworkMapper) fetchPlatformDataFromAPI() error {
	// Use the cryptocurrency map endpoint to get platform information
	// This endpoint includes platform data which has native token information
	mapURL := "https://pro-api.coinmarketcap.com/v1/cryptocurrency/map?listing_status=active&limit=5000"

	req, err := http.NewRequest("GET", mapURL, nil)
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

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse the response to extract platform data
	var mapResponse struct {
		Status struct {
			ErrorCode    int    `json:"error_code"`
			ErrorMessage string `json:"error_message"`
		} `json:"status"`
		Data []struct {
			ID       int    `json:"id"`
			Name     string `json:"name"`
			Symbol   string `json:"symbol"`
			Slug     string `json:"slug"`
			Platform *struct {
				ID           int    `json:"id"`
				Name         string `json:"name"`
				Symbol       string `json:"symbol"`
				Slug         string `json:"slug"`
				TokenAddress string `json:"token_address"`
			} `json:"platform"`
		} `json:"data"`
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if err := json.Unmarshal(body, &mapResponse); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if mapResponse.Status.ErrorCode != 0 {
		return fmt.Errorf("API error: %s", mapResponse.Status.ErrorMessage)
	}

	// Build platform cache from native tokens (tokens with no platform = native blockchain tokens)
	for _, crypto := range mapResponse.Data {
		if crypto.Platform == nil {
			// This is a native blockchain token
			platformData := CoinMarketCapNetwork{
				ID:     crypto.ID,
				Name:   crypto.Name,
				Symbol: crypto.Symbol,
				Slug:   crypto.Slug,
			}
			nm.networkPlatformCache[crypto.Slug] = platformData
		}
	}

	// Also try to map known network slugs to their native tokens
	// This maps our network cache to the native tokens we found
	for networkID, networkSlug := range nm.networksCache {
		if platformData, exists := nm.networkPlatformCache[networkSlug]; exists {
			nm.nativeTokenCache[networkID] = platformData.Symbol
		}
	}

	return nil
}
