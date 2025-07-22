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
	httpClient          *http.Client
	staticContextProvider *StaticContextProvider
	discoveredIcons     map[string]string // address -> icon URL
	verbose             bool
}

// NewIconResolver creates a new icon resolver
func NewIconResolver(staticContextProvider *StaticContextProvider) *IconResolver {
	return &IconResolver{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		staticContextProvider: staticContextProvider,
		discoveredIcons:      make(map[string]string),
		verbose:              false,
	}
}

// SetVerbose enables or disables verbose logging
func (ir *IconResolver) SetVerbose(verbose bool) {
	ir.verbose = verbose
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

	// Clear previous discoveries
	ir.discoveredIcons = make(map[string]string)

	if ir.verbose {
		fmt.Printf("IconResolver: Processing %d contract addresses\n", len(contractAddresses))
	}

	// Process each contract address
	for _, address := range contractAddresses {
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
	req, err := http.NewRequestWithContext(ctx, "HEAD", iconURL, nil)
	if err != nil {
		return false
	}

	resp, err := ir.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	// Consider 200 and 304 (not modified) as successful
	return resp.StatusCode == 200 || resp.StatusCode == 304
}

// GetAnnotationContext provides annotation context with discovered icons
func (ir *IconResolver) GetAnnotationContext(ctx context.Context, baggage map[string]interface{}) *models.AnnotationContext {
	annotationContext := &models.AnnotationContext{
		Items: make([]models.AnnotationContextItem, 0),
	}

	// Get discovered icons from baggage
	discoveredIcons, ok := baggage["discovered_icons"].(map[string]string)
	if !ok || len(discoveredIcons) == 0 {
		return annotationContext
	}

	// Get token metadata for additional context
	tokenMetadata, _ := baggage["token_metadata"].(map[string]*TokenMetadata)

	// Add icon context for discovered icons
	for address, iconURL := range discoveredIcons {
		var metadata *TokenMetadata
		if tokenMetadata != nil {
			metadata, _ = tokenMetadata[strings.ToLower(address)]
		}

		name := "Unknown Token"
		symbol := "TOKEN"
		description := "Token contract"

		if metadata != nil {
			if metadata.Name != "" {
				name = metadata.Name
			}
			if metadata.Symbol != "" {
				symbol = metadata.Symbol
			}
			description = fmt.Sprintf("%s token", metadata.Type)
			if metadata.Decimals > 0 {
				description += fmt.Sprintf(" with %d decimals", metadata.Decimals)
			}
		}

		// Add by address
		annotationContext.AddItem(models.AnnotationContextItem{
			Type:        "token",
			Value:       address,
			Name:        fmt.Sprintf("%s (%s)", name, symbol),
			Icon:        iconURL,
			Description: description,
			Metadata: map[string]interface{}{
				"address":     address,
				"icon_source": "trustwallet_github",
			},
		})

		// Add by symbol for easier matching
		if symbol != "TOKEN" {
			annotationContext.AddItem(models.AnnotationContextItem{
				Type:        "token",
				Value:       symbol,
				Name:        fmt.Sprintf("%s (%s)", name, symbol),
				Icon:        iconURL,
				Description: description,
				Metadata: map[string]interface{}{
					"address":     address,
					"icon_source": "trustwallet_github",
				},
			})
		}
	}

	if ir.verbose && len(annotationContext.Items) > 0 {
		fmt.Printf("IconResolver: Provided %d annotation context items with icons\n", len(annotationContext.Items))
	}

	return annotationContext
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