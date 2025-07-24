package tools

import (
	"context"
	"fmt"
	"os"
	"time"
)

// PriceService provides a unified interface for fetching token prices from various sources
type PriceService interface {
	// GetTokenPrice fetches price for an ERC20 token by contract address
	GetTokenPrice(ctx context.Context, networkID int64, contractAddress string) (*TokenPriceResult, error)

	// GetNativeTokenPrice fetches price for native token (ETH, MATIC, etc.)
	GetNativeTokenPrice(ctx context.Context, networkID int64) (float64, error)

	// GetNativeTokenSymbol returns the native token symbol for a network
	GetNativeTokenSymbol(networkID int64) string

	// IsAvailable returns whether the price service is configured and available
	IsAvailable() bool
}

// TokenPriceResult represents price information for a token
type TokenPriceResult struct {
	Symbol      string    `json:"symbol"`
	Price       float64   `json:"price"`
	Contract    string    `json:"contract_address,omitempty"`
	LastUpdated time.Time `json:"last_updated"`
	PriceSource string    `json:"price_source"`
}

// CoingeckoPriceService implements PriceService using Coingecko APIs
type CoingeckoPriceService struct {
	client *CoingeckoClient
}

// NewCoingeckoPriceService creates a new price service using Coingecko
func NewCoingeckoPriceService(cache Cache, verbose bool) *CoingeckoPriceService {
	apiKey := os.Getenv("COINGECKO_API_KEY")
	client := NewCoingeckoClient(apiKey, cache, verbose)

	return &CoingeckoPriceService{
		client: client,
	}
}

// GetTokenPrice fetches price for an ERC20 token by contract address
func (p *CoingeckoPriceService) GetTokenPrice(ctx context.Context, networkID int64, contractAddress string) (*TokenPriceResult, error) {
	result, err := p.client.GetTokenPrice(ctx, networkID, contractAddress)
	if err != nil {
		return nil, err
	}

	// Convert to unified TokenPriceResult
	return &TokenPriceResult{
		Symbol:      result.Symbol,
		Price:       result.Price,
		Contract:    result.Contract,
		LastUpdated: result.LastUpdated,
		PriceSource: result.PriceSource,
	}, nil
}

// GetNativeTokenPrice fetches price for native token
func (p *CoingeckoPriceService) GetNativeTokenPrice(ctx context.Context, networkID int64) (float64, error) {
	return p.client.GetNativeTokenPrice(ctx, networkID)
}

// GetNativeTokenSymbol returns the native token symbol for a network
func (p *CoingeckoPriceService) GetNativeTokenSymbol(networkID int64) string {
	return p.client.GetNativeTokenSymbol(networkID)
}

// IsAvailable returns whether the price service is configured and available
func (p *CoingeckoPriceService) IsAvailable() bool {
	return p.client.IsAvailable()
}

// NewPriceService creates the appropriate price service based on configuration
func NewPriceService(cache Cache, verbose bool) PriceService {
	// For now, we only have Coingecko implementation
	// In the future, we could check configuration and return different implementations
	return NewCoingeckoPriceService(cache, verbose)
}

// GetNetworkSlugFromEnv gets the Coingecko network slug from environment variables
func GetNetworkSlugFromEnv(networkID int64) (string, error) {
	// Look for network-specific environment variable
	// Pattern: COINGECKO_NETWORK_SLUG_CHAIN_<CHAIN_ID>=<SLUG>
	envKey := fmt.Sprintf("COINGECKO_NETWORK_SLUG_CHAIN_%d", networkID)
	if slug := os.Getenv(envKey); slug != "" {
		return slug, nil
	}

	// Fallback to common network mappings if not in env
	fallbackMappings := map[int64]string{
		1:     "eth",       // Ethereum
		137:   "polygon",   // Polygon
		56:    "bsc",       // BSC
		43114: "avalanche", // Avalanche
		250:   "fantom",    // Fantom
		42161: "arbitrum",  // Arbitrum
		10:    "optimism",  // Optimism
		8453:  "base",      // Base
		25:    "cronos",    // Cronos
	}

	if slug, exists := fallbackMappings[networkID]; exists {
		return slug, nil
	}

	return "", fmt.Errorf("network ID %d not supported - add COINGECKO_NETWORK_SLUG_CHAIN_%d=<slug> to environment", networkID, networkID)
}
