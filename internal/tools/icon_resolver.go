package tools

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/txplain/txplain/internal/models"
)

// IconResolver discovers token icons from TrustWallet's GitHub repository
type IconResolver struct {
	httpClient            *http.Client
	staticContextProvider *StaticContextProvider
	discoveredIcons       map[string]string // address -> icon URL
	verbose               bool
	cache                 Cache // Cache for icon URLs
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
	return []string{"abi_resolver", "static_context_provider"}
}

// Process discovers icons for contract addresses
func (ir *IconResolver) Process(ctx context.Context, baggage map[string]interface{}) error {
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

		// Try both original address and checksummed version
		iconURLs := []string{
			ir.buildTrustWalletIconURL(address),
			ir.buildTrustWalletIconURL(ir.toChecksumAddress(address)),
		}

		iconFound := false
		for _, iconURL := range iconURLs {
			if ir.checkIconExists(ctx, iconURL) {
				ir.discoveredIcons[strings.ToLower(address)] = iconURL
				if ir.verbose {
					fmt.Printf("IconResolver: Found icon for %s at %s\n", address, iconURL)
				}
				// Send progress update when we find an icon
				if hasProgress {
					progress := fmt.Sprintf("Found icon: %s", address[:10]+"...")
					progressTracker.UpdateComponent("icon_resolver", models.ComponentGroupEnrichment, "Loading Token Icons", models.ComponentStatusRunning, progress)
				}
				iconFound = true
				break
			}
		}

		if !iconFound && ir.verbose {
			fmt.Printf("IconResolver: No icon found for %s\n", address)
		}

		// Add small delay to be respectful to GitHub
		time.Sleep(100 * time.Millisecond)
	}

	// Send final progress update with results summary
	if hasProgress {
		if len(ir.discoveredIcons) > 0 {
			progress := fmt.Sprintf("Completed: Found %d icons out of %d addresses", len(ir.discoveredIcons), len(contractAddresses))
			progressTracker.UpdateComponent("icon_resolver", models.ComponentGroupEnrichment, "Loading Token Icons", models.ComponentStatusFinished, progress)
		} else {
			progress := fmt.Sprintf("Completed: No new icons found for %d addresses", len(contractAddresses))
			progressTracker.UpdateComponent("icon_resolver", models.ComponentGroupEnrichment, "Loading Token Icons", models.ComponentStatusFinished, progress)
		}
	}

	// Add discovered icons to baggage
	baggage["discovered_icons"] = ir.discoveredIcons

	if ir.verbose {
		fmt.Printf("IconResolver: Discovered %d new icons\n", len(ir.discoveredIcons))
	}

	return nil
}

// hasIconInCSV checks if the address already has an icon in the static context provider
func (ir *IconResolver) hasIconInCSV(address string) bool {
	if ir.staticContextProvider == nil {
		return false
	}

	tokenInfo, exists := ir.staticContextProvider.GetTokenInfo(address)
	return exists && tokenInfo.Icon != ""
}

// buildTrustWalletIconURL constructs the TrustWallet GitHub URL for a token icon
func (ir *IconResolver) buildTrustWalletIconURL(address string) string {
	return fmt.Sprintf("https://raw.githubusercontent.com/trustwallet/assets/refs/heads/master/blockchains/ethereum/assets/%s/logo.png", address)
}

// toChecksumAddress converts an address to EIP-55 checksum format
func (ir *IconResolver) toChecksumAddress(address string) string {
	// Remove 0x prefix if present
	addr := strings.ToLower(address)
	if strings.HasPrefix(addr, "0x") {
		addr = addr[2:]
	}

	// Simple checksum implementation (basic version)
	// For production, you might want to use a proper Ethereum utils library
	checksum := ""
	for i, char := range addr {
		if char >= '0' && char <= '9' {
			checksum += string(char)
		} else if char >= 'a' && char <= 'f' {
			// Simple heuristic: uppercase every other letter for basic checksum
			if i%2 == 0 {
				checksum += strings.ToUpper(string(char))
			} else {
				checksum += string(char)
			}
		}
	}

	return "0x" + checksum
}

// checkIconExists performs a HEAD request to check if the icon URL exists
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
	// Icon resolver discovers icons from external sources
	// No general knowledge to contribute to RAG
	return ragContext
}
