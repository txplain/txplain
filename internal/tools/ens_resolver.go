package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/txplain/txplain/internal/models"
	"github.com/txplain/txplain/internal/rpc"
)

// ENSResolver resolves ENS names for addresses found in transaction data
type ENSResolver struct {
	rpcClient *rpc.Client
	verbose   bool
	cache     Cache // Cache for ENS lookups
}

// NewENSResolver creates a new ENS resolver
func NewENSResolver() *ENSResolver {
	return &ENSResolver{
		verbose: false,
		cache:   nil, // Set via SetCache
	}
}

// SetRPCClient sets the RPC client for ENS resolution
func (e *ENSResolver) SetRPCClient(client *rpc.Client) {
	e.rpcClient = client
}

// SetCache sets the cache instance for the ENS resolver
func (e *ENSResolver) SetCache(cache Cache) {
	e.cache = cache
}

// SetVerbose enables or disables verbose logging
func (e *ENSResolver) SetVerbose(verbose bool) {
	e.verbose = verbose
}

// Name returns the tool name
func (e *ENSResolver) Name() string {
	return "ens_resolver"
}

// Description returns the tool description
func (e *ENSResolver) Description() string {
	return "Resolves ENS names for all addresses found in transaction data"
}

// Dependencies returns the tools this processor depends on
func (e *ENSResolver) Dependencies() []string {
	return []string{"monetary_value_enricher"} // Run after monetary enrichment
}

// Process implements the Tool interface
func (e *ENSResolver) Process(ctx context.Context, baggage map[string]interface{}) error {
	if e.verbose {
		fmt.Println("\n" + strings.Repeat("🏷️", 60))
		fmt.Println("🔍 ENS RESOLVER: Starting ENS name resolution")
		fmt.Println(strings.Repeat("🏷️", 60))
	}

	if e.rpcClient == nil {
		if e.verbose {
			fmt.Println("❌ RPC client not set")
			fmt.Println(strings.Repeat("🏷️", 60) + "\n")
		}
		return fmt.Errorf("RPC client not set")
	}

	// Extract all addresses from transaction data
	addresses := e.extractAllAddresses(baggage)

	// Convert map to slice for debug display
	addressList := make([]string, 0, len(addresses))
	for addr := range addresses {
		addressList = append(addressList, addr)
	}

	if e.verbose {
		fmt.Printf("📊 Found %d unique addresses to resolve\n", len(addresses))
		if len(addresses) > 0 {
			fmt.Println("🏠 ADDRESSES TO RESOLVE:")
			for i, addr := range addressList {
				if i < 10 { // Limit display to first 10 addresses
					fmt.Printf("   %d. %s\n", i+1, addr)
				} else if i == 10 {
					fmt.Printf("   ... and %d more\n", len(addressList)-10)
					break
				}
			}
		}
	}

	if len(addresses) == 0 {
		if e.verbose {
			fmt.Println("⚠️  No addresses found, skipping ENS resolution")
			fmt.Println(strings.Repeat("🏷️", 60) + "\n")
		}
		baggage["ens_names"] = make(map[string]string)
		return nil
	}

	// Get progress tracker from baggage if available
	progressTracker, hasProgress := baggage["progress_tracker"].(*models.ProgressTracker)

	if e.verbose {
		fmt.Println("🔄 Resolving ENS names...")
	}

	ensNames := make(map[string]string)
	successCount := 0

	// Resolve ENS names for each address with granular progress updates
	for i, address := range addressList {
		// Send progress update for each address being resolved
		if hasProgress {
			progress := fmt.Sprintf("Checking address %d of %d: %s", i+1, len(addressList), address[:10]+"...")
			progressTracker.UpdateComponent("ens_resolver", models.ComponentGroupEnrichment, "Resolving ENS Names", models.ComponentStatusRunning, progress)
		}

		if e.verbose {
			fmt.Printf("   [%d/%d] Resolving %s...", i+1, len(addressList), address)
		}

		var ensName string
		var err error

		// Check cache first if available
		if e.cache != nil {
			networkID := int64(1) // Default to Ethereum mainnet for ENS
			if rawData, ok := baggage["raw_data"].(map[string]interface{}); ok {
				if nid, ok := rawData["network_id"].(float64); ok {
					networkID = int64(nid)
				}
			}
			
			cacheKey := fmt.Sprintf(ENSNameKeyPattern, networkID, strings.ToLower(address))
			if err := e.cache.GetJSON(ctx, cacheKey, &ensName); err == nil {
				if e.verbose {
					fmt.Printf(" ✅ (cached) %s\n", ensName)
				}
			} else {
				// Cache miss, make RPC call
				ensName, err = e.rpcClient.ResolveENSName(ctx, address)
				if err != nil {
					if e.verbose {
						fmt.Printf(" ❌ Failed: %v\n", err)
					}
					continue // Skip this address if resolution fails
				}
				
				// Cache successful result (including empty results to avoid repeated lookups)
				if cacheErr := e.cache.SetJSON(ctx, cacheKey, ensName, &ENSTTLDuration); cacheErr != nil {
					if e.verbose {
						fmt.Printf(" ⚠️ Cache store failed: %v\n", cacheErr)
					}
				}
			}
		} else {
			// No cache, make RPC call directly
			ensName, err = e.rpcClient.ResolveENSName(ctx, address)
			if err != nil {
				if e.verbose {
					fmt.Printf(" ❌ Failed: %v\n", err)
				}
				continue // Skip this address if resolution fails
			}
		}

		if ensName != "" {
			ensNames[address] = ensName
			successCount++
			if e.verbose {
				fmt.Printf(" ✅ %s\n", ensName)
			}
			// Send progress update when we find an ENS name
			if hasProgress {
				progress := fmt.Sprintf("Found ENS name: %s → %s", address[:10]+"...", ensName)
				progressTracker.UpdateComponent("ens_resolver", models.ComponentGroupEnrichment, "Resolving ENS Names", models.ComponentStatusRunning, progress)
			}
		} else if e.verbose {
			fmt.Printf(" ⚪ No ENS name\n")
		}
	}

	// Send final progress update with results summary
	if hasProgress {
		if successCount > 0 {
			progress := fmt.Sprintf("Completed: Found %d ENS names out of %d addresses", successCount, len(addresses))
			progressTracker.UpdateComponent("ens_resolver", models.ComponentGroupEnrichment, "Resolving ENS Names", models.ComponentStatusFinished, progress)
		} else {
			progress := fmt.Sprintf("Completed: No ENS names found for %d addresses", len(addresses))
			progressTracker.UpdateComponent("ens_resolver", models.ComponentGroupEnrichment, "Resolving ENS Names", models.ComponentStatusFinished, progress)
		}
	}

	if e.verbose {
		fmt.Printf("✅ Successfully resolved %d/%d ENS names\n", successCount, len(addresses))

		// Show summary of resolved names
		if len(ensNames) > 0 {
			fmt.Println("\n📋 RESOLVED ENS NAMES:")
			for addr, ensName := range ensNames {
				fmt.Printf("   • %s → %s\n", addr[:10]+"...", ensName)
			}
		}

		fmt.Println("\n" + strings.Repeat("🏷️", 60))
		fmt.Println("✅ ENS RESOLVER: Completed successfully")
		fmt.Println(strings.Repeat("🏷️", 60) + "\n")
	}

	baggage["ens_names"] = ensNames
	return nil
}

// extractAllAddresses extracts all unique addresses from transaction data
func (e *ENSResolver) extractAllAddresses(baggage map[string]interface{}) map[string]bool {
	addresses := make(map[string]bool)

	// Extract from raw transaction data
	if rawData, ok := baggage["raw_data"].(map[string]interface{}); ok {
		// Transaction sender/receiver
		if receipt, ok := rawData["receipt"].(map[string]interface{}); ok {
			if from, ok := receipt["from"].(string); ok && from != "" {
				addresses[strings.ToLower(from)] = true
			}
			if to, ok := receipt["to"].(string); ok && to != "" {
				addresses[strings.ToLower(to)] = true
			}
		}
	}

	// Extract from transfers
	if transfers, ok := baggage["transfers"].([]models.TokenTransfer); ok {
		for _, transfer := range transfers {
			if transfer.From != "" {
				// Remove leading zeros padding for proper address format
				cleanFrom := e.cleanAddress(transfer.From)
				if cleanFrom != "" {
					addresses[strings.ToLower(cleanFrom)] = true
				}
			}
			if transfer.To != "" {
				cleanTo := e.cleanAddress(transfer.To)
				if cleanTo != "" {
					addresses[strings.ToLower(cleanTo)] = true
				}
			}
			if transfer.Contract != "" {
				addresses[strings.ToLower(transfer.Contract)] = true
			}
		}
	}

	// Extract from events
	if events, ok := baggage["events"].([]models.Event); ok {
		for _, event := range events {
			if event.Contract != "" {
				addresses[strings.ToLower(event.Contract)] = true
			}

			// Extract from event parameters
			if event.Parameters != nil {
				if from, ok := event.Parameters["from"].(string); ok && from != "" {
					cleanFrom := e.cleanAddress(from)
					if cleanFrom != "" {
						addresses[strings.ToLower(cleanFrom)] = true
					}
				}
				if to, ok := event.Parameters["to"].(string); ok && to != "" {
					cleanTo := e.cleanAddress(to)
					if cleanTo != "" {
						addresses[strings.ToLower(cleanTo)] = true
					}
				}
			}
		}
	}

	// Extract from token metadata
	if tokenMetadata, ok := baggage["token_metadata"].(map[string]*TokenMetadata); ok {
		for address := range tokenMetadata {
			addresses[strings.ToLower(address)] = true
		}
	}

	return addresses
}

// cleanAddress removes padding and ensures proper address format
func (e *ENSResolver) cleanAddress(address string) string {
	if address == "" {
		return ""
	}

	// Handle padded addresses like "0x000000000000000000000000c1757ff0f69aae03f77e64c2adeb76128adfc5bd"
	if strings.HasPrefix(address, "0x") && len(address) == 66 {
		// Extract the last 40 characters (20 bytes) for the actual address
		actualAddress := "0x" + address[len(address)-40:]
		// Validate it's not a zero address
		if actualAddress != "0x0000000000000000000000000000000000000000" {
			return actualAddress
		}
	}

	// Return as-is if already proper format
	if strings.HasPrefix(address, "0x") && len(address) == 42 {
		return address
	}

	return ""
}

// shortenAddress creates a shortened version of an address
func (e *ENSResolver) shortenAddress(address string) string {
	if len(address) != 42 || !strings.HasPrefix(address, "0x") {
		return address
	}
	return address[:6] + "..." + address[len(address)-4:]
}

// formatAddressWithENS formats an address with optional ENS name
func (e *ENSResolver) formatAddressWithENS(address string, ensNames map[string]string) string {
	cleanAddr := e.cleanAddress(address)
	if cleanAddr == "" {
		return address
	}

	shortened := e.shortenAddress(cleanAddr)

	if ensName, exists := ensNames[strings.ToLower(cleanAddr)]; exists {
		return fmt.Sprintf("%s (%s)", shortened, ensName)
	}

	return shortened
}

// GetPromptContext provides ENS context for LLM prompts
func (e *ENSResolver) GetPromptContext(ctx context.Context, baggage map[string]interface{}) string {
	ensNames, ok := baggage["ens_names"].(map[string]string)
	if !ok {
		ensNames = make(map[string]string) // Empty map if no ENS data
	}

	// Get all addresses from transaction data for formatting
	allAddresses := e.extractAllAddresses(baggage)
	if len(allAddresses) == 0 {
		return ""
	}

	var contextParts []string

	// Add ENS names section if any were resolved
	if len(ensNames) > 0 {
		contextParts = append(contextParts, "### ENS Names Resolved:")
		for address, ensName := range ensNames {
			shortAddr := e.shortenAddress(address)
			contextParts = append(contextParts, fmt.Sprintf("- %s: %s", shortAddr, ensName))
		}
		contextParts = append(contextParts, "")
	}

	// Add address formatting mappings for ALL addresses in the transaction
	contextParts = append(contextParts, "### Address Formatting Guide:")
	contextParts = append(contextParts, "Use these formatted addresses in your explanation instead of full addresses:")

	for fullAddress := range allAddresses {
		cleanAddr := e.cleanAddress(fullAddress)
		if cleanAddr == "" {
			continue
		}

		formattedAddr := e.formatAddressWithENS(cleanAddr, ensNames)
		contextParts = append(contextParts, fmt.Sprintf("- %s → %s", cleanAddr, formattedAddr))
	}

	// Add formatting instructions
	contextParts = append(contextParts, "")
	contextParts = append(contextParts, "### Address Usage Instructions:")
	contextParts = append(contextParts, "- ALWAYS use the formatted addresses from the guide above")
	contextParts = append(contextParts, "- Format: 0xabcd...1234 or 0xabcd...1234 (ens-name.eth)")
	contextParts = append(contextParts, "- Never use full 42-character addresses in explanations")
	contextParts = append(contextParts, "- Never use padded 66-character addresses")

	return strings.Join(contextParts, "\n")
}

// GetRagContext provides RAG context for ENS information
func (e *ENSResolver) GetRagContext(ctx context.Context, baggage map[string]interface{}) *RagContext {
	ragContext := NewRagContext()

	ensNames, ok := baggage["ens_names"].(map[string]string)
	if !ok || len(ensNames) == 0 {
		return ragContext
	}

	// Add ENS mappings to RAG context for searchability
	for address, ensName := range ensNames {
		ragContext.AddItem(RagContextItem{
			ID:      fmt.Sprintf("ens_%s", address),
			Type:    "ens",
			Title:   fmt.Sprintf("ENS Name: %s", ensName),
			Content: fmt.Sprintf("Address %s is mapped to ENS name %s", address, ensName),
			Metadata: map[string]interface{}{
				"address":  address,
				"ens_name": ensName,
			},
			Keywords:  []string{ensName, address, "ens", "name"},
			Relevance: 0.6,
		})
	}

	return ragContext
}
