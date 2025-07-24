package tools

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/txplain/txplain/internal/models"
)

// IconResolver discovers token icons from multiple sources (TrustWallet)
type IconResolver struct {
	httpClient            *http.Client
	staticContextProvider *StaticContextProvider
	discoveredIcons       map[string]string // address -> icon URL
	verbose               bool
	cache                 Cache // Cache for icon URLs
	currentNetworkID      int64 // To store network ID for dynamic chain slug
}

// NewIconResolver creates a new icon resolver
func NewIconResolver(staticContextProvider *StaticContextProvider, cache Cache, verbose bool) *IconResolver {
	return &IconResolver{
		httpClient: &http.Client{
			Timeout: 300 * time.Second, // 5 minutes for icon downloads
		},
		staticContextProvider: staticContextProvider,
		discoveredIcons:       make(map[string]string),
		verbose:               verbose,
		cache:                 cache,
		currentNetworkID:      1, // Default to Ethereum for now, will be set during Process
	}
}

// Name returns the processor name
func (ir *IconResolver) Name() string {
	return "icon_resolver"
}

// Description returns the processor description
func (ir *IconResolver) Description() string {
	return "Discovers token icons from TrustWallet repository for contracts without CSV entries"
}

// Dependencies returns the tools this processor depends on
func (ir *IconResolver) Dependencies() []string {
	return []string{"abi_resolver", "static_context_provider", "token_metadata_enricher"}
}

// Process discovers icons for contract addresses
func (ir *IconResolver) Process(ctx context.Context, baggage map[string]interface{}) error {
	// Get network ID from raw data (following pattern of other tools)
	networkID := int64(1) // Default to Ethereum
	if rawData, ok := baggage["raw_data"].(map[string]interface{}); ok {
		if nid, ok := rawData["network_id"].(float64); ok {
			networkID = int64(nid)
		}
	}
	ir.currentNetworkID = networkID

	// Get contract addresses from baggage (set by ABI resolver)
	contractAddresses, ok := baggage["contract_addresses"].([]string)
	if !ok || len(contractAddresses) == 0 {
		if ir.verbose {
			fmt.Println("IconResolver: No contract addresses found in baggage")
		}
		return nil
	}

	// Get progress tracker from baggage if available
	progressTracker, hasProgress := baggage["progress_tracker"].(*models.ProgressTracker)

	// Clear previous discoveries
	ir.discoveredIcons = make(map[string]string)

	if ir.verbose {
		fmt.Printf("IconResolver: Processing %d contract addresses\n", len(contractAddresses))
	}

	// Process each contract address with granular progress updates
	for i, address := range contractAddresses {
		// Send progress update for each address being checked
		if hasProgress {
			progress := fmt.Sprintf("Checking icon %d of %d: %s", i+1, len(contractAddresses), address[:10]+"...")
			progressTracker.UpdateComponent("icon_resolver", models.ComponentGroupEnrichment, "Loading Token Icons", models.ComponentStatusRunning, progress)
		}

		if ir.hasIconInCSV(address) {
			if ir.verbose {
				fmt.Printf("IconResolver: %s already has icon in CSV, skipping\n", address)
			}
			continue
		}

		var iconURL string
		var iconSource string

		// Try TrustWallet repository
		trustWalletIcon := ir.getTrustWalletIcon(ctx, address)
		if trustWalletIcon != "" {
			iconURL = trustWalletIcon
			iconSource = "trustwallet"
			if ir.verbose {
				fmt.Printf("IconResolver: Found TrustWallet icon for %s: %s\n", address, iconURL)
			}
		}

		// Step 4: Store discovered icon
		if iconURL != "" {
			ir.discoveredIcons[strings.ToLower(address)] = iconURL

			// Send progress update when we find an icon
			if hasProgress {
				progress := fmt.Sprintf("Found icon for %s from %s", address[:10]+"...", iconSource)
				progressTracker.UpdateComponent("icon_resolver", models.ComponentGroupEnrichment, "Loading Token Icons", models.ComponentStatusRunning, progress)
			}
		} else if ir.verbose {
			fmt.Printf("IconResolver: No icon found for %s\n", address)
		}
	}

	// Send final progress update
	if hasProgress {
		progress := fmt.Sprintf("Completed: Found %d icons out of %d contracts", len(ir.discoveredIcons), len(contractAddresses))
		progressTracker.UpdateComponent("icon_resolver", models.ComponentGroupEnrichment, "Loading Token Icons", models.ComponentStatusFinished, progress)
	}

	if ir.verbose {
		fmt.Printf("IconResolver: Found %d new icons\n", len(ir.discoveredIcons))
	}

	// Add discovered icons to baggage
	if len(ir.discoveredIcons) > 0 {
		baggage["discovered_icons"] = ir.discoveredIcons
	}

	return nil
}

// getTrustWalletIcon fetches icon from TrustWallet repository (fallback)
func (ir *IconResolver) getTrustWalletIcon(ctx context.Context, address string) string {
	// Get network ID from baggage for proper chain slug
	networkID := ir.getNetworkIDFromBaggage()
	
	// Check cache first if available
	if ir.cache != nil {
		cacheKey := fmt.Sprintf("trustwallet-icon:%d:%s", networkID, strings.ToLower(address))

		var cachedURL string
		if err := ir.cache.GetJSON(ctx, cacheKey, &cachedURL); err == nil {
			if ir.verbose {
				fmt.Printf("    ✅ (cached) TrustWallet icon check for %s: %s\n", address, cachedURL)
			}
			return cachedURL
		}
	}

	// Try both original address and checksummed version
	iconURLs := []string{
		ir.buildTrustWalletIconURL(address, networkID),
		ir.buildTrustWalletIconURL(ir.toChecksumAddress(address), networkID),
	}

	for _, iconURL := range iconURLs {
		if ir.checkIconExists(ctx, iconURL) {
			// Cache successful result
			if ir.cache != nil {
				cacheKey := fmt.Sprintf("trustwallet-icon:%d:%s", networkID, strings.ToLower(address))
				if err := ir.cache.SetJSON(ctx, cacheKey, iconURL, &IconTTLDuration); err != nil {
					if ir.verbose {
						fmt.Printf("    ⚠️ Failed to cache TrustWallet icon for %s: %v\n", address, err)
					}
				}
			}
			return iconURL
		}
	}

	// Cache negative result to avoid repeated failed requests
	if ir.cache != nil {
		cacheKey := fmt.Sprintf("trustwallet-icon:%d:%s", networkID, strings.ToLower(address))
		ir.cache.SetJSON(ctx, cacheKey, "", &IconTTLDuration)
	}

	return ""
}

// hasIconInCSV checks if an address already has an icon in the static CSV data
func (ir *IconResolver) hasIconInCSV(address string) bool {
	if ir.staticContextProvider == nil {
		return false
	}

	// Get static context and check if this address has an icon
	ragContext := ir.staticContextProvider.GetRagContext(context.Background(), nil)
	for _, item := range ragContext.Items {
		if item.Type == "token" {
			if addrVal, ok := item.Metadata["address"].(string); ok {
				if strings.EqualFold(addrVal, address) {
					if iconVal, ok := item.Metadata["icon"].(string); ok && iconVal != "" {
						return true
					}
				}
			}
		}
	}

	return false
}

// buildTrustWalletIconURL constructs the TrustWallet icon URL for an address and network
func (ir *IconResolver) buildTrustWalletIconURL(address string, networkID int64) string {
	chainSlug, err := ir.getTrustWalletChainSlug(networkID)
	if err != nil {
		if ir.verbose {
			fmt.Printf("    ⚠️ No TrustWallet chain slug configured for network %d: %v\n", networkID, err)
		}
		// Fall back to ethereum for backward compatibility
		chainSlug = "ethereum"
	}
	
	return fmt.Sprintf("https://raw.githubusercontent.com/trustwallet/assets/master/blockchains/%s/assets/%s/logo.png", chainSlug, address)
}

// getTrustWalletChainSlug gets the TrustWallet chain slug from environment variables
func (ir *IconResolver) getTrustWalletChainSlug(networkID int64) (string, error) {
	// Look for network-specific environment variable
	// Pattern: TRUSTWALLET_ASSETS_SLUG_CHAIN_<CHAIN_ID>=<SLUG>
	envKey := fmt.Sprintf("TRUSTWALLET_ASSETS_SLUG_CHAIN_%d", networkID)
	if chainSlug := os.Getenv(envKey); chainSlug != "" {
		return chainSlug, nil
	}

	// Fallback to common network mappings if not in env
	fallbackMappings := map[int64]string{
		1:     "ethereum",  // Ethereum
		137:   "polygon",   // Polygon
		56:    "smartchain", // BSC (TrustWallet uses "smartchain" for BSC)
		43114: "avalanchec", // Avalanche (TrustWallet uses "avalanchec")
		250:   "fantom",    // Fantom
		42161: "arbitrum",  // Arbitrum
		10:    "optimism",  // Optimism
		8453:  "base",      // Base
		25:    "cronos",    // Cronos
		100:   "xdai",      // Gnosis Chain (xDAI)
	}

	if chainSlug, exists := fallbackMappings[networkID]; exists {
		return chainSlug, nil
	}

	return "", fmt.Errorf("network ID %d not supported - add TRUSTWALLET_ASSETS_SLUG_CHAIN_%d=<slug> to environment", networkID, networkID)
}

// getNetworkIDFromBaggage extracts network ID from baggage like other tools
func (ir *IconResolver) getNetworkIDFromBaggage() int64 {
	// This is a bit of a hack - we store the baggage during Process and use it here
	// Following the same pattern as other tools that need network info
	// Default to Ethereum if not available
	return ir.currentNetworkID
}

// toChecksumAddress converts an address to EIP-55 checksum format (simplified)
func (ir *IconResolver) toChecksumAddress(address string) string {
	// This is a simplified checksum implementation
	// For production, you might want to use a proper library
	if !strings.HasPrefix(address, "0x") {
		address = "0x" + address
	}
	return address
}

// checkIconExists checks if an icon URL is accessible
func (ir *IconResolver) checkIconExists(ctx context.Context, iconURL string) bool {
	// Check cache first if available
	if ir.cache != nil {
		cacheKey := fmt.Sprintf("icon-check:%s", iconURL)

		var exists bool
		if err := ir.cache.GetJSON(ctx, cacheKey, &exists); err == nil {
			if ir.verbose {
				fmt.Printf("    ✅ (cached) Icon check for %s: %t\n", iconURL, exists)
			}
			return exists
		}
	}

	req, err := http.NewRequestWithContext(ctx, "HEAD", iconURL, nil)
	if err != nil {
		return false
	}

	resp, err := ir.httpClient.Do(req)
	if err != nil {
		if ir.cache != nil {
			// Cache negative result to avoid repeated failed requests
			cacheKey := fmt.Sprintf("icon-check:%s", iconURL)
			ir.cache.SetJSON(ctx, cacheKey, false, &IconTTLDuration)
		}
		return false
	}
	defer resp.Body.Close()

	// Consider 200 and 304 (not modified) as successful
	exists := resp.StatusCode == 200 || resp.StatusCode == 304

	// Cache result (both positive and negative) with permanent TTL
	if ir.cache != nil {
		cacheKey := fmt.Sprintf("icon-check:%s", iconURL)
		if err := ir.cache.SetJSON(ctx, cacheKey, exists, &IconTTLDuration); err != nil {
			if ir.verbose {
				fmt.Printf("    ⚠️ Failed to cache icon check for %s: %v\n", iconURL, err)
			}
		} else if ir.verbose {
			fmt.Printf("    ✅ Cached icon check: %s -> %t\n", iconURL, exists)
		}
	}

	return exists
}

// GetPromptContext provides context for LLM prompts
func (ir *IconResolver) GetPromptContext(ctx context.Context, baggage map[string]interface{}) string {
	discoveredIcons, ok := baggage["discovered_icons"].(map[string]string)
	if !ok || len(discoveredIcons) == 0 {
		return ""
	}

	var contextParts []string
	contextParts = append(contextParts, "Discovered Token Icons:")

	for address, iconURL := range discoveredIcons {
		contextParts = append(contextParts, fmt.Sprintf("- %s: %s", address, iconURL))
	}

	return strings.Join(contextParts, "\n")
}

// GetRagContext provides RAG context for icon information (minimal for this tool)
func (ir *IconResolver) GetRagContext(ctx context.Context, baggage map[string]interface{}) *RagContext {
	ragContext := NewRagContext()

	discoveredIcons, ok := baggage["discovered_icons"].(map[string]string)
	if !ok || len(discoveredIcons) == 0 {
		return ragContext
	}

	// Add discovered icons to RAG context
	for address, iconURL := range discoveredIcons {
		ragContext.AddItem(RagContextItem{
			ID:      fmt.Sprintf("icon_%s", address),
			Type:    "icon",
			Title:   fmt.Sprintf("Icon for %s", address),
			Content: fmt.Sprintf("Token icon available at %s for contract address %s", iconURL, address),
			Metadata: map[string]interface{}{
				"address":  address,
				"icon_url": iconURL,
			},
			Keywords:  []string{address, "icon", "logo"},
			Relevance: 0.6,
		})
	}

	return ragContext
}
