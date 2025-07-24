package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// CoinMarketCapClient centralizes all CoinMarketCap API interactions
type CoinMarketCapClient struct {
	apiKey        string
	httpClient    *http.Client
	networkMapper *NetworkMapper
	cache         Cache
	verbose       bool
}

// TokenInfo represents comprehensive token information from CoinMarketCap
type TokenInfo struct {
	ID          int                    `json:"id"`
	Name        string                 `json:"name"`
	Symbol      string                 `json:"symbol"`
	Slug        string                 `json:"slug"`
	Logo        string                 `json:"logo"`
	Description string                 `json:"description"`
	Category    string                 `json:"category"`
	Website     string                 `json:"website"`
	URLs        map[string]interface{} `json:"urls"`
	Platform    struct {
		ID           int    `json:"id"`
		Name         string `json:"name"`
		Symbol       string `json:"symbol"`
		Slug         string `json:"slug"`
		TokenAddress string `json:"token_address"`
	} `json:"platform,omitempty"`
}

// CMCTokenInfo represents essential token information from CoinMarketCap
type CMCTokenInfo struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Symbol      string `json:"symbol"`
	Logo        string `json:"logo"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
	Website     string `json:"website,omitempty"`
}

// NewCoinMarketCapClient creates a new centralized CoinMarketCap client
func NewCoinMarketCapClient(apiKey string, cache Cache, verbose bool) *CoinMarketCapClient {
	networkMapper := NewNetworkMapper(apiKey, cache)

	return &CoinMarketCapClient{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 300 * time.Second, // 5 minutes for API calls
		},
		networkMapper: networkMapper,
		cache:         cache,
		verbose:       verbose,
	}
}

// IsAvailable returns whether the CoinMarketCap API is available (has API key)
func (c *CoinMarketCapClient) IsAvailable() bool {
	return c.apiKey != ""
}

// GetTokenInfo fetches comprehensive token information by contract address
func (c *CoinMarketCapClient) GetTokenInfo(ctx context.Context, contractAddress string) (*CMCTokenInfo, error) {
	if !c.IsAvailable() {
		return nil, fmt.Errorf("CoinMarketCap API key not configured")
	}

	// Check cache first
	if c.cache != nil {
		cacheKey := fmt.Sprintf("cmc-token-info:%s", strings.ToLower(contractAddress))

		var cachedInfo CMCTokenInfo
		if err := c.cache.GetJSON(ctx, cacheKey, &cachedInfo); err == nil {
			if c.verbose {
				fmt.Printf("  ✅ (cached) CoinMarketCap token info for %s: %s (%s)\n", contractAddress, cachedInfo.Name, cachedInfo.Symbol)
			}
			return &cachedInfo, nil
		}
	}

	// Find token ID first
	tokenID, err := c.findTokenByAddress(ctx, contractAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to find token: %w", err)
	}

	// Get detailed token info
	tokenInfo, err := c.getTokenInfoByID(ctx, tokenID)
	if err != nil {
		return nil, fmt.Errorf("failed to get token info: %w", err)
	}

	// Cache successful result
	if c.cache != nil {
		cacheKey := fmt.Sprintf("cmc-token-info:%s", strings.ToLower(contractAddress))
		if err := c.cache.SetJSON(ctx, cacheKey, tokenInfo, &MetadataTTLDuration); err != nil {
			if c.verbose {
				fmt.Printf("  ⚠️ Failed to cache token info for %s: %v\n", contractAddress, err)
			}
		}
	}

	return tokenInfo, nil
}

// GetTokenPrice fetches comprehensive token price by search parameters
func (c *CoinMarketCapClient) GetTokenPrice(ctx context.Context, symbol, name, contractAddress string, convert string) (*TokenPrice, error) {
	if !c.IsAvailable() {
		return nil, fmt.Errorf("CoinMarketCap API key not configured")
	}

	if convert == "" {
		convert = "USD"
	}

	// Find token ID
	tokenID, err := c.findToken(ctx, symbol, name, contractAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to find token: %w", err)
	}

	// Get price data
	return c.getTokenPriceByID(ctx, tokenID, convert)
}

// GetDEXPrice fetches DEX price data for a token
func (c *CoinMarketCapClient) GetDEXPrice(ctx context.Context, contractAddress string, networkID int64) ([]DEXPriceData, error) {
	if !c.IsAvailable() {
		return nil, fmt.Errorf("CoinMarketCap API key not configured")
	}

	// Check cache first
	if c.cache != nil {
		cacheKey := fmt.Sprintf("dex-price:%d:%s", networkID, strings.ToLower(contractAddress))

		var cachedData []DEXPriceData
		if err := c.cache.GetJSON(ctx, cacheKey, &cachedData); err == nil {
			if c.verbose {
				fmt.Printf("  ✅ (cached) DEX price data for %s: %d pairs\n", contractAddress, len(cachedData))
			}
			return cachedData, nil
		}
	}

	// Get network slug
	blockchainSlug, err := c.networkMapper.GetNetworkSlug(networkID)
	if err != nil {
		return nil, fmt.Errorf("failed to get network slug: %w", err)
	}

	// Build query parameters
	params := url.Values{}
	params.Set("network_slug", blockchainSlug)
	params.Set("token_address", strings.ToLower(contractAddress))
	params.Set("convert", "USD")
	params.Set("sort", "liquidity_usd")
	params.Set("limit", "10")

	// Make API request
	dexURL := fmt.Sprintf("https://pro-api.coinmarketcap.com/v4/dex/pairs/quotes/latest?%s", params.Encode())
	body, err := c.makeRequest(ctx, dexURL)
	if err != nil {
		return nil, err
	}

	// Parse response
	var dexResp struct {
		Status struct {
			ErrorCode    int    `json:"error_code"`
			ErrorMessage string `json:"error_message"`
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

	if err := json.Unmarshal(body, &dexResp); err != nil {
		return nil, fmt.Errorf("failed to parse DEX response: %w", err)
	}

	if dexResp.Status.ErrorCode != 0 {
		return nil, fmt.Errorf("DEX API error: %s", dexResp.Status.ErrorMessage)
	}

	// Convert to our format
	var dexData []DEXPriceData
	for _, pair := range dexResp.Data {
		dexData = append(dexData, DEXPriceData{
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
		})
	}

	// Cache successful result
	if c.cache != nil {
		cacheKey := fmt.Sprintf("dex-price:%d:%s", networkID, strings.ToLower(contractAddress))
		if err := c.cache.SetJSON(ctx, cacheKey, dexData, &PriceTTLDuration); err != nil {
			if c.verbose {
				fmt.Printf("  ⚠️ Failed to cache DEX price data for %s: %v\n", contractAddress, err)
			}
		}
	}

	return dexData, nil
}

// GetDEXListingInfo fetches DEX listing metadata
func (c *CoinMarketCapClient) GetDEXListingInfo(ctx context.Context, contractAddress string, networkID int64) (*DEXListingInfo, error) {
	if !c.IsAvailable() {
		return nil, fmt.Errorf("CoinMarketCap API key not configured")
	}

	// Check cache first
	if c.cache != nil {
		cacheKey := fmt.Sprintf("dex-listing:%d:%s", networkID, strings.ToLower(contractAddress))

		var cachedListing DEXListingInfo
		if err := c.cache.GetJSON(ctx, cacheKey, &cachedListing); err == nil {
			if c.verbose {
				fmt.Printf("  ✅ (cached) DEX listing info for %s: %s (%s)\n", contractAddress, cachedListing.Name, cachedListing.Symbol)
			}
			return &cachedListing, nil
		}
	}

	// Get network slug
	blockchainSlug, err := c.networkMapper.GetNetworkSlug(networkID)
	if err != nil {
		return nil, fmt.Errorf("failed to get network slug: %w", err)
	}

	// Build query parameters - only fetch essential fields
	params := url.Values{}
	params.Set("network_slug", blockchainSlug)
	params.Set("contract_address", strings.ToLower(contractAddress))
	params.Set("aux", "logo,description") // Only fetch essential extra fields

	// Make API request
	listingURL := fmt.Sprintf("https://pro-api.coinmarketcap.com/v4/dex/listings/info?%s", params.Encode())
	body, err := c.makeRequest(ctx, listingURL)
	if err != nil {
		return nil, err
	}

	// Parse response
	var listingResp struct {
		Status struct {
			ErrorCode    int    `json:"error_code"`
			ErrorMessage string `json:"error_message"`
		} `json:"status"`
		Data []struct {
			ID           int    `json:"id"`
			Name         string `json:"name"`
			Symbol       string `json:"symbol"`
			Slug         string `json:"slug"`
			Logo         string `json:"logo"`
			Description  string `json:"description"`
			DateLaunched string `json:"date_launched"`
			Notice       string `json:"notice"`
			Status       string `json:"status"`
			Category     string `json:"category"`
			URLs         struct {
				Website      []string `json:"website"`
				TechnicalDoc []string `json:"technical_doc"`
				Explorer     []string `json:"explorer"`
				SourceCode   []string `json:"source_code"`
				MessageBoard []string `json:"message_board"`
				Chat         []string `json:"chat"`
				Facebook     []string `json:"facebook"`
				Twitter      []string `json:"twitter"`
				Reddit       []string `json:"reddit"`
			} `json:"urls"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &listingResp); err != nil {
		return nil, fmt.Errorf("failed to parse DEX listing response: %w", err)
	}

	if listingResp.Status.ErrorCode != 0 {
		return nil, fmt.Errorf("DEX listing API error: %s", listingResp.Status.ErrorMessage)
	}

	if len(listingResp.Data) == 0 {
		return nil, fmt.Errorf("no DEX listing found for contract address")
	}

	tokenData := listingResp.Data[0]

	// Convert URLs to map
	urls := map[string]interface{}{
		"website":       tokenData.URLs.Website,
		"technical_doc": tokenData.URLs.TechnicalDoc,
		"explorer":      tokenData.URLs.Explorer,
		"source_code":   tokenData.URLs.SourceCode,
		"message_board": tokenData.URLs.MessageBoard,
		"chat":          tokenData.URLs.Chat,
		"facebook":      tokenData.URLs.Facebook,
		"twitter":       tokenData.URLs.Twitter,
		"reddit":        tokenData.URLs.Reddit,
	}

	listing := &DEXListingInfo{
		ID:              tokenData.ID,
		Name:            tokenData.Name,
		Symbol:          tokenData.Symbol,
		Slug:            tokenData.Slug,
		Logo:            tokenData.Logo,
		Description:     tokenData.Description,
		DateLaunched:    tokenData.DateLaunched,
		Notice:          tokenData.Notice,
		Status:          tokenData.Status,
		Category:        tokenData.Category,
		URLs:            urls,
		ContractAddress: contractAddress,
		NetworkSlug:     blockchainSlug,
	}

	// Cache successful result
	if c.cache != nil {
		cacheKey := fmt.Sprintf("dex-listing:%d:%s", networkID, strings.ToLower(contractAddress))
		if err := c.cache.SetJSON(ctx, cacheKey, listing, &MetadataTTLDuration); err != nil {
			if c.verbose {
				fmt.Printf("  ⚠️ Failed to cache DEX listing info for %s: %v\n", contractAddress, err)
			}
		}
	}

	return listing, nil
}

// GetNativeTokenPrice fetches price for native tokens (ETH, MATIC, etc.)
func (c *CoinMarketCapClient) GetNativeTokenPrice(ctx context.Context, symbol string) (float64, error) {
	if !c.IsAvailable() {
		return 0, fmt.Errorf("CoinMarketCap API key not configured")
	}

	// Check cache first
	if c.cache != nil {
		cacheKey := fmt.Sprintf("native-token-price:%s:USD", strings.ToUpper(symbol))

		var cachedPrice float64
		if err := c.cache.GetJSON(ctx, cacheKey, &cachedPrice); err == nil {
			if c.verbose {
				fmt.Printf("  ✅ (cached) Native token price for %s: $%.6f\n", symbol, cachedPrice)
			}
			return cachedPrice, nil
		}
	}

	// Build query parameters
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("convert", "USD")

	// Make API request
	quoteURL := fmt.Sprintf("https://pro-api.coinmarketcap.com/v1/cryptocurrency/quotes/latest?%s", params.Encode())
	body, err := c.makeRequest(ctx, quoteURL)
	if err != nil {
		return 0, err
	}

	// Parse response
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

	// Extract price
	tokenData, exists := response.Data[symbol]
	if !exists {
		return 0, fmt.Errorf("token data not found for symbol %s", symbol)
	}

	quoteData, exists := tokenData.Quote["USD"]
	if !exists {
		return 0, fmt.Errorf("USD quote not found for symbol %s", symbol)
	}

	price := quoteData.Price

	// Cache successful result
	if c.cache != nil {
		cacheKey := fmt.Sprintf("native-token-price:%s:USD", strings.ToUpper(symbol))
		if err := c.cache.SetJSON(ctx, cacheKey, price, &PriceTTLDuration); err != nil {
			if c.verbose {
				fmt.Printf("  ⚠️ Failed to cache native token price for %s: %v\n", symbol, err)
			}
		}
	}

	return price, nil
}

// GetNativeTokenSymbol returns the native token symbol for a given network ID
func (c *CoinMarketCapClient) GetNativeTokenSymbol(networkID int64) string {
	return c.networkMapper.GetNativeTokenSymbol(networkID)
}

// Private helper methods

// makeRequest makes an authenticated HTTP request to CoinMarketCap API
func (c *CoinMarketCapClient) makeRequest(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-CMC_PRO_API_KEY", c.apiKey)
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

// findToken searches for a token using symbol, name, or contract address
func (c *CoinMarketCapClient) findToken(ctx context.Context, symbol, name, contractAddress string) (int, error) {
	// Check cache first
	if c.cache != nil {
		cacheKeyParams := fmt.Sprintf("%s:%s:%s", symbol, name, strings.ToLower(contractAddress))
		cacheKey := fmt.Sprintf(CMCMappingKeyPattern, 0, cacheKeyParams)

		var cachedTokenID int
		if err := c.cache.GetJSON(ctx, cacheKey, &cachedTokenID); err == nil {
			if c.verbose {
				fmt.Printf("  ✅ (cached) Token mapping: %s -> ID %d\n", cacheKeyParams, cachedTokenID)
			}
			return cachedTokenID, nil
		}
	}

	// Build query parameters
	params := url.Values{}
	params.Set("listing_status", "active")
	params.Set("limit", "5000")
	if symbol != "" {
		params.Set("symbol", strings.ToUpper(symbol))
	}

	// Make API request
	mapURL := fmt.Sprintf("https://pro-api.coinmarketcap.com/v1/cryptocurrency/map?%s", params.Encode())
	body, err := c.makeRequest(ctx, mapURL)
	if err != nil {
		return 0, err
	}

	// Parse response
	var mapResp struct {
		Status struct {
			ErrorCode    int    `json:"error_code"`
			ErrorMessage string `json:"error_message"`
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
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &mapResp); err != nil {
		return 0, fmt.Errorf("failed to parse response: %w", err)
	}

	if mapResp.Status.ErrorCode != 0 {
		return 0, fmt.Errorf("API error: %s", mapResp.Status.ErrorMessage)
	}

	// Find best match
	bestMatch := c.findBestMatch(mapResp.Data, symbol, name, contractAddress)
	if bestMatch == nil {
		return 0, fmt.Errorf("no matching token found")
	}

	tokenID := bestMatch.ID

	// Cache successful result
	if c.cache != nil {
		cacheKeyParams := fmt.Sprintf("%s:%s:%s", symbol, name, strings.ToLower(contractAddress))
		cacheKey := fmt.Sprintf(CMCMappingKeyPattern, 0, cacheKeyParams)

		if err := c.cache.SetJSON(ctx, cacheKey, tokenID, &MetadataTTLDuration); err != nil {
			if c.verbose {
				fmt.Printf("  ⚠️ Failed to cache token mapping %s: %v\n", cacheKeyParams, err)
			}
		}
	}

	return tokenID, nil
}

// findTokenByAddress searches for a token specifically by contract address
func (c *CoinMarketCapClient) findTokenByAddress(ctx context.Context, contractAddress string) (int, error) {
	return c.findToken(ctx, "", "", contractAddress)
}

// findBestMatch finds the best matching token from search results
func (c *CoinMarketCapClient) findBestMatch(tokens []struct {
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

	// Second priority: exact symbol match
	if symbol != "" {
		symbol = strings.ToUpper(symbol)
		for _, token := range tokens {
			if token.IsActive == 1 && strings.ToUpper(token.Symbol) == symbol {
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

	return nil
}

// getTokenInfoByID fetches essential token info by ID
func (c *CoinMarketCapClient) getTokenInfoByID(ctx context.Context, tokenID int) (*CMCTokenInfo, error) {
	// Build query parameters - only request the fields we need
	params := url.Values{}
	params.Set("id", strconv.Itoa(tokenID))
	params.Set("aux", "urls,logo,description") // Only fetch essential extra fields

	// Make API request
	infoURL := fmt.Sprintf("https://pro-api.coinmarketcap.com/v1/cryptocurrency/info?%s", params.Encode())
	body, err := c.makeRequest(ctx, infoURL)
	if err != nil {
		return nil, err
	}

	// Parse response
	var infoResp struct {
		Status struct {
			ErrorCode    int    `json:"error_code"`
			ErrorMessage string `json:"error_message"`
		} `json:"status"`
		Data map[string]struct {
			ID          int    `json:"id"`
			Name        string `json:"name"`
			Symbol      string `json:"symbol"`
			Category    string `json:"category"`
			Description string `json:"description"`
			Slug        string `json:"slug"`
			Logo        string `json:"logo"`
			URLs        struct {
				Website      []string `json:"website"`
				TechnicalDoc []string `json:"technical_doc"`
				Explorer     []string `json:"explorer"`
				SourceCode   []string `json:"source_code"`
				MessageBoard []string `json:"message_board"`
				Chat         []string `json:"chat"`
				Facebook     []string `json:"facebook"`
				Twitter      []string `json:"twitter"`
				Reddit       []string `json:"reddit"`
			} `json:"urls"`
			Platform struct {
				ID           int    `json:"id"`
				Name         string `json:"name"`
				Symbol       string `json:"symbol"`
				Slug         string `json:"slug"`
				TokenAddress string `json:"token_address"`
			} `json:"platform,omitempty"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &infoResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if infoResp.Status.ErrorCode != 0 {
		return nil, fmt.Errorf("API error: %s", infoResp.Status.ErrorMessage)
	}

	// Extract token data
	tokenIDStr := strconv.Itoa(tokenID)
	tokenData, exists := infoResp.Data[tokenIDStr]
	if !exists {
		return nil, fmt.Errorf("token data not found in response")
	}

	// Get first website URL if available
	website := ""
	if len(tokenData.URLs.Website) > 0 {
		website = tokenData.URLs.Website[0]
	}

	return &CMCTokenInfo{
		ID:          tokenData.ID,
		Name:        tokenData.Name,
		Symbol:      tokenData.Symbol,
		Logo:        tokenData.Logo,
		Description: tokenData.Description,
		Category:    tokenData.Category,
		Website:     website,
	}, nil
}

// getTokenPriceByID fetches token price by ID
func (c *CoinMarketCapClient) getTokenPriceByID(ctx context.Context, tokenID int, convert string) (*TokenPrice, error) {
	// Check cache first
	if c.cache != nil {
		cacheKey := fmt.Sprintf(TokenPriceKeyPattern, 0, fmt.Sprintf("id:%d:%s", tokenID, convert))

		var cachedPrice TokenPrice
		if err := c.cache.GetJSON(ctx, cacheKey, &cachedPrice); err == nil {
			if c.verbose {
				fmt.Printf("  ✅ (cached) Token price: ID %d -> $%.6f\n", tokenID, cachedPrice.Price)
			}
			return &cachedPrice, nil
		}
	}

	// Build query parameters
	params := url.Values{}
	params.Set("id", strconv.Itoa(tokenID))
	params.Set("convert", convert)

	// Make API request
	quoteURL := fmt.Sprintf("https://pro-api.coinmarketcap.com/v1/cryptocurrency/quotes/latest?%s", params.Encode())
	body, err := c.makeRequest(ctx, quoteURL)
	if err != nil {
		return nil, err
	}

	// Parse response
	var quoteResp struct {
		Status struct {
			ErrorCode    int    `json:"error_code"`
			ErrorMessage string `json:"error_message"`
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

	// Extract quote data
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
		PriceSource:       "CEX",
	}

	// Cache successful result
	if c.cache != nil {
		cacheKey := fmt.Sprintf(TokenPriceKeyPattern, 0, fmt.Sprintf("id:%d:%s", tokenID, convert))
		if err := c.cache.SetJSON(ctx, cacheKey, result, &PriceTTLDuration); err != nil {
			if c.verbose {
				fmt.Printf("  ⚠️ Failed to cache token price ID %d: %v\n", tokenID, err)
			}
		}
	}

	return result, nil
}
