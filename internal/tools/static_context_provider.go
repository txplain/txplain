package tools

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"strings"

	"github.com/txplain/txplain/internal/models"
)

// StaticContextProvider loads context from CSV files for common tokens, protocols, addresses
type StaticContextProvider struct {
	tokens    map[string]models.AnnotationContextItem // address -> token info
	protocols map[string]models.AnnotationContextItem // name -> protocol info  
	addresses map[string]models.AnnotationContextItem // address -> address info
	verbose   bool
}

// NewStaticContextProvider creates a new static context provider
func NewStaticContextProvider() *StaticContextProvider {
	provider := &StaticContextProvider{
		tokens:    make(map[string]models.AnnotationContextItem),
		protocols: make(map[string]models.AnnotationContextItem),
		addresses: make(map[string]models.AnnotationContextItem),
		verbose:   false,
	}
	
	// Load data from CSV files on initialization
	provider.loadTokens()
	provider.loadProtocols()
	provider.loadAddresses()
	
	return provider
}

// SetVerbose enables or disables verbose logging
func (scp *StaticContextProvider) SetVerbose(verbose bool) {
	scp.verbose = verbose
}

// Name returns the processor name
func (scp *StaticContextProvider) Name() string {
	return "static_context_provider"
}

// Description returns the processor description
func (scp *StaticContextProvider) Description() string {
	return "Loads static context data from CSV files for tokens, protocols, and addresses"
}

// Dependencies returns the tools this processor depends on
func (scp *StaticContextProvider) Dependencies() []string {
	return []string{} // No dependencies, runs first
}

// Process implements BaggageProcessor interface
func (scp *StaticContextProvider) Process(ctx context.Context, baggage map[string]interface{}) error {
	// Static context provider doesn't modify baggage directly
	// Its data is accessed via GetAnnotationContext when needed
	return nil
}

// GetAnnotationContext implements AnnotationContextProvider interface
func (scp *StaticContextProvider) GetAnnotationContext(ctx context.Context, baggage map[string]interface{}) *models.AnnotationContext {
	context := &models.AnnotationContext{
		Items: make([]models.AnnotationContextItem, 0),
	}

	// Add all loaded tokens (both by address and by symbol for easier matching)
	for _, item := range scp.tokens {
		context.Items = append(context.Items, item)
		
		// Also add entries by symbol for easier matching in text
		if symbol, ok := item.Metadata["symbol"].(string); ok && symbol != "" {
			symbolItem := item
			symbolItem.Value = symbol // Change value to symbol for text matching
			context.Items = append(context.Items, symbolItem)
		}
	}

	// Add all loaded protocols
	for _, item := range scp.protocols {
		context.Items = append(context.Items, item)
	}

	// Add all loaded addresses
	for _, item := range scp.addresses {
		context.Items = append(context.Items, item)
	}

	if scp.verbose {
		fmt.Printf("StaticContextProvider: Provided %d context items\n", len(context.Items))
	}

	return context
}

// loadTokens loads token information from CSV file
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
	}

	if scp.verbose {
		fmt.Printf("StaticContextProvider: Loaded %d tokens from %s\n", len(scp.tokens), filename)
	}
}

// loadProtocols loads protocol information from CSV file
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

		scp.protocols[strings.ToLower(name)] = models.AnnotationContextItem{
			Type:        "protocol",
			Value:       name,
			Name:        name,
			Icon:        icon,
			Link:        link,
			Description: description,
		}
	}

	if scp.verbose {
		fmt.Printf("StaticContextProvider: Loaded %d protocols from %s\n", len(scp.protocols), filename)
	}
}

// loadAddresses loads well-known address information from CSV file
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
	}

	if scp.verbose {
		fmt.Printf("StaticContextProvider: Loaded %d addresses from %s\n", len(scp.addresses), filename)
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