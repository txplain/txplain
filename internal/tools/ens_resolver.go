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
}

// NewENSResolver creates a new ENS resolver
func NewENSResolver() *ENSResolver {
	return &ENSResolver{}
}

// SetRPCClient sets the RPC client for ENS resolution
func (e *ENSResolver) SetRPCClient(client *rpc.Client) {
	e.rpcClient = client
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
	if e.rpcClient == nil {
		return fmt.Errorf("RPC client not set")
	}

	// Extract all addresses from transaction data
	addresses := e.extractAllAddresses(baggage)

	// Convert map to slice for debug display
	addressList := make([]string, 0, len(addresses))
	for addr := range addresses {
		addressList = append(addressList, addr)
	}

	if len(addresses) == 0 {
		baggage["ens_names"] = make(map[string]string)
		return nil
	}

	ensNames := make(map[string]string)

	// Resolve ENS names for each address
	for address := range addresses {
		ensName, err := e.rpcClient.ResolveENSName(ctx, address)
		if err != nil {
			continue // Skip this address if resolution fails
		}

		if ensName != "" {
			ensNames[address] = ensName
		}
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
