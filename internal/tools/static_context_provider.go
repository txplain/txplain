package tools

import (
	"context"
	"crypto/md5"
	"encoding/csv"
	"fmt"
	"os"
	"strings"

	"github.com/txplain/txplain/internal/models"
)

// StaticContextProvider loads context from CSV files for RAG and lightweight prompts
type StaticContextProvider struct {
	tokens    map[string]models.AnnotationContextItem // address -> token info
	protocols map[string]models.AnnotationContextItem // name -> protocol info
	addresses map[string]models.AnnotationContextItem // address -> address info

	// RAG-specific data
	ragTokens    map[string]RagContextItem // address -> RAG token data
	ragProtocols map[string]RagContextItem // name -> RAG protocol data
	ragAddresses map[string]RagContextItem // address -> RAG address data

	verbose bool
}

// NewStaticContextProvider creates a new static context provider
func NewStaticContextProvider(verbose bool) *StaticContextProvider {
	provider := &StaticContextProvider{
		tokens:    make(map[string]models.AnnotationContextItem),
		protocols: make(map[string]models.AnnotationContextItem),
		addresses: make(map[string]models.AnnotationContextItem),

		// Initialize RAG storage
		ragTokens:    make(map[string]RagContextItem),
		ragProtocols: make(map[string]RagContextItem),
		ragAddresses: make(map[string]RagContextItem),

		verbose: verbose,
	}

	// Load data from CSV files on initialization
	provider.loadTokens()
	provider.loadProtocols()
	provider.loadAddresses()

	return provider
}

// Name returns the processor name
func (scp *StaticContextProvider) Name() string {
	return "static_context_provider"
}

// Description returns the processor description
func (scp *StaticContextProvider) Description() string {
	return "Loads static context data from CSV files for RAG storage and lightweight prompts"
}

// Dependencies returns the tools this processor depends on
func (scp *StaticContextProvider) Dependencies() []string {
	return []string{} // No dependencies, runs first
}

// Process implements BaggageProcessor interface
func (scp *StaticContextProvider) Process(ctx context.Context, baggage map[string]interface{}) error {
	// Static context provider doesn't modify baggage directly
	// Its data is accessed via GetPromptContext and GetRagContext when needed
	return nil
}

// GetPromptContext provides minimal, always-relevant context for prompts
// Heavy CSV data is now moved to GetRagContext for selective retrieval
func (scp *StaticContextProvider) GetPromptContext(ctx context.Context, baggage map[string]interface{}) string {
	// Only provide summary statistics and availability info, not full data
	var contextParts []string

	if len(scp.tokens) > 0 {
		contextParts = append(contextParts, fmt.Sprintf("### STATIC DATA AVAILABLE:\n- %d known tokens in RAG database", len(scp.tokens)))
	}

	if len(scp.protocols) > 0 {
		contextParts = append(contextParts, fmt.Sprintf("- %d known protocols in RAG database", len(scp.protocols)))
	}

	if len(scp.addresses) > 0 {
		contextParts = append(contextParts, fmt.Sprintf("- %d known addresses in RAG database", len(scp.addresses)))
	}

	if len(contextParts) > 0 {
		contextParts = append(contextParts, "\nNote: Detailed information available via RAG retrieval system")
		return strings.Join(contextParts, "\n")
	}

	return ""
}

// GetRagContext provides detailed CSV data for RAG storage and retrieval
// This replaces the heavy data that was previously in GetPromptContext
func (scp *StaticContextProvider) GetRagContext(ctx context.Context, baggage map[string]interface{}) *RagContext {
	ragContext := NewRagContext()

	// Add all token data to RAG context
	for _, ragItem := range scp.ragTokens {
		ragContext.AddItem(ragItem)
	}

	// Add all protocol data to RAG context
	for _, ragItem := range scp.ragProtocols {
		ragContext.AddItem(ragItem)
	}

	// Add all address data to RAG context
	for _, ragItem := range scp.ragAddresses {
		ragContext.AddItem(ragItem)
	}

	return ragContext
}

// generateID creates a consistent ID for RAG items
func (scp *StaticContextProvider) generateID(itemType, key string) string {
	hash := md5.Sum([]byte(fmt.Sprintf("static_%s_%s", itemType, key)))
	return fmt.Sprintf("static_%s_%x", itemType, hash[:8])
}

// loadTokens loads token information from CSV file for both annotation and RAG contexts
func (scp *StaticContextProvider) loadTokens() {
	filename := "data/tokens.csv"
	if !scp.fileExists(filename) {
		if scp.verbose {
			fmt.Printf("StaticContextProvider: Token file %s not found, skipping\n", filename)
		}
		return
	}

	file, err := os.Open(filename)
	if err != nil {
		if scp.verbose {
			fmt.Printf("StaticContextProvider: Error opening token file: %v\n", err)
		}
		return
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		if scp.verbose {
			fmt.Printf("StaticContextProvider: Error reading token CSV: %v\n", err)
		}
		return
	}

	// Expected format: address,symbol,name,decimals,icon_url,description
	for i, record := range records {
		if i == 0 || len(record) < 4 { // Skip header row or incomplete rows
			continue
		}

		address := strings.ToLower(record[0])
		symbol := record[1]
		name := record[2]
		decimals := record[3]
		icon := ""
		description := ""

		if len(record) > 4 {
			icon = record[4]
		}
		if len(record) > 5 {
			description = record[5]
		}

		// Legacy annotation context (kept for backward compatibility)
		scp.tokens[address] = models.AnnotationContextItem{
			Type:        "token",
			Value:       address,
			Name:        fmt.Sprintf("%s (%s)", name, symbol),
			Icon:        icon,
			Description: description,
			Metadata: map[string]interface{}{
				"symbol":   symbol,
				"name":     name,
				"decimals": decimals,
			},
		}

		// NEW: RAG context item with rich, searchable content
		ragContent := fmt.Sprintf(`Token: %s (%s)
Contract Address: %s
Symbol: %s
Name: %s
Decimals: %s
Description: %s
Icon: %s

This is a well-known token with established metadata. Use this information for accurate token identification and display.`,
			name, symbol, address, symbol, name, decimals, description, icon)

		keywords := []string{
			strings.ToLower(symbol),
			strings.ToLower(name),
			strings.ToLower(address),
			"token", "erc20",
		}

		// Add description words as keywords if available
		if description != "" {
			descWords := strings.Fields(strings.ToLower(description))
			keywords = append(keywords, descWords...)
		}

		scp.ragTokens[address] = RagContextItem{
			ID:      scp.generateID("token", address),
			Type:    "token",
			Title:   fmt.Sprintf("%s (%s) Token", name, symbol),
			Content: ragContent,
			Metadata: map[string]interface{}{
				"address":     address,
				"symbol":      symbol,
				"name":        name,
				"decimals":    decimals,
				"icon":        icon,
				"description": description,
				"source":      "csv_tokens",
			},
			Keywords:  keywords,
			Relevance: 0.8, // High relevance for known tokens
		}
	}

	if scp.verbose {
		fmt.Printf("StaticContextProvider: Loaded %d tokens from %s (%d RAG items)\n", len(scp.tokens), filename, len(scp.ragTokens))
	}
}

// loadProtocols loads protocol information from CSV file for both annotation and RAG contexts
func (scp *StaticContextProvider) loadProtocols() {
	filename := "data/protocols.csv"
	if !scp.fileExists(filename) {
		if scp.verbose {
			fmt.Printf("StaticContextProvider: Protocol file %s not found, skipping\n", filename)
		}
		return
	}

	file, err := os.Open(filename)
	if err != nil {
		if scp.verbose {
			fmt.Printf("StaticContextProvider: Error opening protocol file: %v\n", err)
		}
		return
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		if scp.verbose {
			fmt.Printf("StaticContextProvider: Error reading protocol CSV: %v\n", err)
		}
		return
	}

	// Expected format: name,icon_url,website_url,description
	for i, record := range records {
		if i == 0 || len(record) < 2 { // Skip header row or incomplete rows
			continue
		}

		name := record[0]
		icon := record[1]
		link := ""
		description := ""

		if len(record) > 2 {
			link = record[2]
		}
		if len(record) > 3 {
			description = record[3]
		}

		// Legacy annotation context (kept for backward compatibility)
		scp.protocols[strings.ToLower(name)] = models.AnnotationContextItem{
			Type:        "protocol",
			Value:       name,
			Name:        name,
			Icon:        icon,
			Link:        link,
			Description: description,
		}

		// NEW: RAG context item with rich, searchable content
		ragContent := fmt.Sprintf(`Protocol: %s
Website: %s
Icon: %s
Description: %s

This is a well-known DeFi/Web3 protocol. Use this information for accurate protocol identification and linking.`,
			name, link, icon, description)

		keywords := []string{
			strings.ToLower(name),
			"protocol", "defi", "web3",
		}

		// Add description words as keywords if available
		if description != "" {
			descWords := strings.Fields(strings.ToLower(description))
			keywords = append(keywords, descWords...)
		}

		scp.ragProtocols[strings.ToLower(name)] = RagContextItem{
			ID:      scp.generateID("protocol", name),
			Type:    "protocol",
			Title:   fmt.Sprintf("%s Protocol", name),
			Content: ragContent,
			Metadata: map[string]interface{}{
				"name":        name,
				"website":     link,
				"icon":        icon,
				"description": description,
				"source":      "csv_protocols",
			},
			Keywords:  keywords,
			Relevance: 0.9, // Very high relevance for known protocols
		}
	}

	if scp.verbose {
		fmt.Printf("StaticContextProvider: Loaded %d protocols from %s (%d RAG items)\n", len(scp.protocols), filename, len(scp.ragProtocols))
	}
}

// loadAddresses loads well-known address information from CSV file for both annotation and RAG contexts
func (scp *StaticContextProvider) loadAddresses() {
	filename := "data/addresses.csv"
	if !scp.fileExists(filename) {
		if scp.verbose {
			fmt.Printf("StaticContextProvider: Address file %s not found, skipping\n", filename)
		}
		return
	}

	file, err := os.Open(filename)
	if err != nil {
		if scp.verbose {
			fmt.Printf("StaticContextProvider: Error opening address file: %v\n", err)
		}
		return
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		if scp.verbose {
			fmt.Printf("StaticContextProvider: Error reading address CSV: %v\n", err)
		}
		return
	}

	// Expected format: address,name,type,description,explorer_url
	for i, record := range records {
		if i == 0 || len(record) < 3 { // Skip header row or incomplete rows
			continue
		}

		address := strings.ToLower(record[0])
		name := record[1]
		addressType := record[2]
		description := ""
		link := ""

		if len(record) > 3 {
			description = record[3]
		}
		if len(record) > 4 {
			link = record[4]
		}

		// Legacy annotation context (kept for backward compatibility)
		scp.addresses[address] = models.AnnotationContextItem{
			Type:        "address",
			Value:       address,
			Name:        name,
			Link:        link,
			Description: description,
			Metadata: map[string]interface{}{
				"address_type": addressType,
			},
		}

		// NEW: RAG context item with rich, searchable content
		ragContent := fmt.Sprintf(`Address: %s
Name: %s
Type: %s
Description: %s
Explorer: %s

This is a well-known blockchain address. Use this information for accurate address identification and labeling.`,
			address, name, addressType, description, link)

		keywords := []string{
			strings.ToLower(address),
			strings.ToLower(name),
			strings.ToLower(addressType),
			"address", "contract", "wallet",
		}

		// Add description words as keywords if available
		if description != "" {
			descWords := strings.Fields(strings.ToLower(description))
			keywords = append(keywords, descWords...)
		}

		scp.ragAddresses[address] = RagContextItem{
			ID:      scp.generateID("address", address),
			Type:    "address",
			Title:   fmt.Sprintf("%s Address (%s)", name, addressType),
			Content: ragContent,
			Metadata: map[string]interface{}{
				"address":     address,
				"name":        name,
				"type":        addressType,
				"description": description,
				"explorer":    link,
				"source":      "csv_addresses",
			},
			Keywords:  keywords,
			Relevance: 0.7, // Good relevance for known addresses
		}
	}

	if scp.verbose {
		fmt.Printf("StaticContextProvider: Loaded %d addresses from %s (%d RAG items)\n", len(scp.addresses), filename, len(scp.ragAddresses))
	}
}

// fileExists checks if a file exists
func (scp *StaticContextProvider) fileExists(filename string) bool {
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		return false
	}
	return true
}

// GetTokenInfo retrieves token information by address
func (scp *StaticContextProvider) GetTokenInfo(address string) (models.AnnotationContextItem, bool) {
	item, exists := scp.tokens[strings.ToLower(address)]
	return item, exists
}

// GetProtocolInfo retrieves protocol information by name
func (scp *StaticContextProvider) GetProtocolInfo(name string) (models.AnnotationContextItem, bool) {
	item, exists := scp.protocols[strings.ToLower(name)]
	return item, exists
}

// GetAddressInfo retrieves address information by address
func (scp *StaticContextProvider) GetAddressInfo(address string) (models.AnnotationContextItem, bool) {
	item, exists := scp.addresses[strings.ToLower(address)]
	return item, exists
}
