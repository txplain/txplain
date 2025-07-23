package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

// RAGSearchService provides autonomous search functions for LLMs
type RAGSearchService struct {
	staticProvider *StaticContextProvider
	verbose        bool
}

// NewRAGSearchService creates a new RAG search service
func NewRAGSearchService(staticProvider *StaticContextProvider, verbose bool) *RAGSearchService {
	return &RAGSearchService{
		staticProvider: staticProvider,
		verbose:        verbose,
	}
}

// SearchProtocolsResult represents the result of a protocol search
type SearchProtocolsResult struct {
	Query   string           `json:"query"`
	Results []ProtocolResult `json:"results"`
	Found   int              `json:"found"`
}

type ProtocolResult struct {
	Name        string  `json:"name"`
	Type        string  `json:"type,omitempty"`
	Website     string  `json:"website,omitempty"`
	Description string  `json:"description,omitempty"`
	Icon        string  `json:"icon,omitempty"`
	Confidence  float64 `json:"confidence"`
}

// SearchTokensResult represents the result of a token search
type SearchTokensResult struct {
	Query   string        `json:"query"`
	Results []TokenResult `json:"results"`
	Found   int           `json:"found"`
}

type TokenResult struct {
	Address     string  `json:"address"`
	Symbol      string  `json:"symbol"`
	Name        string  `json:"name"`
	Decimals    string  `json:"decimals,omitempty"`
	Description string  `json:"description,omitempty"`
	Icon        string  `json:"icon,omitempty"`
	Confidence  float64 `json:"confidence"`
}

// SearchAddressesResult represents the result of an address search
type SearchAddressesResult struct {
	Query   string          `json:"query"`
	Results []AddressResult `json:"results"`
	Found   int             `json:"found"`
}

type AddressResult struct {
	Address     string  `json:"address"`
	Name        string  `json:"name"`
	Type        string  `json:"type,omitempty"`
	Description string  `json:"description,omitempty"`
	Explorer    string  `json:"explorer,omitempty"`
	Confidence  float64 `json:"confidence"`
}

// SearchProtocols performs fuzzy search for protocol information
// This function is called autonomously by the LLM when it encounters unknown protocols
func (r *RAGSearchService) SearchProtocols(ctx context.Context, query string) (*SearchProtocolsResult, error) {
	if r.verbose {
		fmt.Printf("RAG Search: Looking for protocols matching '%s'\n", query)
	}

	queryLower := strings.ToLower(query)
	var results []ProtocolResult

	// Get RAG context from static provider
	ragContext := r.staticProvider.GetRagContext(ctx, nil)

	// Search through protocol knowledge
	for _, item := range ragContext.Items {
		if item.Type != "protocol" {
			continue
		}

		confidence := r.calculateFuzzyMatch(queryLower, item)
		if confidence > 0.1 { // Minimum relevance threshold

			// Extract metadata
			name := ""
			website := ""
			icon := ""
			if nameVal, ok := item.Metadata["name"].(string); ok {
				name = nameVal
			}
			if websiteVal, ok := item.Metadata["website"].(string); ok {
				website = websiteVal
			}
			if iconVal, ok := item.Metadata["icon"].(string); ok {
				icon = iconVal
			}

			results = append(results, ProtocolResult{
				Name:        name,
				Type:        "DeFi Protocol", // Could be enhanced with more specific types
				Website:     website,
				Description: r.extractDescription(item.Content),
				Icon:        icon,
				Confidence:  confidence,
			})
		}
	}

	// Sort by confidence (simple bubble sort)
	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Confidence > results[i].Confidence {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	// Limit to top 5 results
	if len(results) > 5 {
		results = results[:5]
	}

	result := &SearchProtocolsResult{
		Query:   query,
		Results: results,
		Found:   len(results),
	}

	if r.verbose {
		fmt.Printf("RAG Search: Found %d protocol matches for '%s'\n", len(results), query)
		for i, res := range results {
			fmt.Printf("  Result[%d]: %s (%.3f confidence)\n", i, res.Name, res.Confidence)
		}
	}

	return result, nil
}

// SearchTokens performs fuzzy search for token information
// This function is called autonomously by the LLM when it encounters unknown tokens
func (r *RAGSearchService) SearchTokens(ctx context.Context, addressOrSymbol string) (*SearchTokensResult, error) {
	if r.verbose {
		fmt.Printf("RAG Search: Looking for tokens matching '%s'\n", addressOrSymbol)
	}

	queryLower := strings.ToLower(addressOrSymbol)
	var results []TokenResult

	// Get RAG context from static provider
	ragContext := r.staticProvider.GetRagContext(ctx, nil)

	// Search through token knowledge
	for _, item := range ragContext.Items {
		if item.Type != "token" {
			continue
		}

		confidence := r.calculateFuzzyMatch(queryLower, item)
		if confidence > 0.1 { // Minimum relevance threshold

			// Extract metadata
			address := ""
			symbol := ""
			name := ""
			decimals := ""
			icon := ""
			if addrVal, ok := item.Metadata["address"].(string); ok {
				address = addrVal
			}
			if symbolVal, ok := item.Metadata["symbol"].(string); ok {
				symbol = symbolVal
			}
			if nameVal, ok := item.Metadata["name"].(string); ok {
				name = nameVal
			}
			if decimalsVal, ok := item.Metadata["decimals"].(string); ok {
				decimals = decimalsVal
			}
			if iconVal, ok := item.Metadata["icon"].(string); ok {
				icon = iconVal
			}

			results = append(results, TokenResult{
				Address:     address,
				Symbol:      symbol,
				Name:        name,
				Decimals:    decimals,
				Description: r.extractDescription(item.Content),
				Icon:        icon,
				Confidence:  confidence,
			})
		}
	}

	// Sort by confidence
	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Confidence > results[i].Confidence {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	// Limit to top 5 results
	if len(results) > 5 {
		results = results[:5]
	}

	result := &SearchTokensResult{
		Query:   addressOrSymbol,
		Results: results,
		Found:   len(results),
	}

	if r.verbose {
		fmt.Printf("RAG Search: Found %d token matches for '%s'\n", len(results), addressOrSymbol)
		for i, res := range results {
			fmt.Printf("  Result[%d]: %s (%s) - %.3f confidence\n", i, res.Name, res.Symbol, res.Confidence)
		}
	}

	return result, nil
}

// SearchAddresses performs fuzzy search for well-known address information
// This function is called autonomously by the LLM when it encounters unknown addresses
func (r *RAGSearchService) SearchAddresses(ctx context.Context, address string) (*SearchAddressesResult, error) {
	if r.verbose {
		fmt.Printf("RAG Search: Looking for addresses matching '%s'\n", address)
	}

	queryLower := strings.ToLower(address)
	var results []AddressResult

	// Get RAG context from static provider
	ragContext := r.staticProvider.GetRagContext(ctx, nil)

	// Search through address knowledge
	for _, item := range ragContext.Items {
		if item.Type != "address" {
			continue
		}

		confidence := r.calculateFuzzyMatch(queryLower, item)
		if confidence > 0.1 { // Minimum relevance threshold

			// Extract metadata
			itemAddress := ""
			name := ""
			addressType := ""
			explorer := ""
			if addrVal, ok := item.Metadata["address"].(string); ok {
				itemAddress = addrVal
			}
			if nameVal, ok := item.Metadata["name"].(string); ok {
				name = nameVal
			}
			if typeVal, ok := item.Metadata["type"].(string); ok {
				addressType = typeVal
			}
			if explorerVal, ok := item.Metadata["explorer"].(string); ok {
				explorer = explorerVal
			}

			results = append(results, AddressResult{
				Address:     itemAddress,
				Name:        name,
				Type:        addressType,
				Description: r.extractDescription(item.Content),
				Explorer:    explorer,
				Confidence:  confidence,
			})
		}
	}

	// Sort by confidence
	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Confidence > results[i].Confidence {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	// Limit to top 5 results
	if len(results) > 5 {
		results = results[:5]
	}

	result := &SearchAddressesResult{
		Query:   address,
		Results: results,
		Found:   len(results),
	}

	if r.verbose {
		fmt.Printf("RAG Search: Found %d address matches for '%s'\n", len(results), address)
		for i, res := range results {
			fmt.Printf("  Result[%d]: %s (%s) - %.3f confidence\n", i, res.Name, res.Type, res.Confidence)
		}
	}

	return result, nil
}

// calculateFuzzyMatch computes fuzzy match score between query and knowledge item
func (r *RAGSearchService) calculateFuzzyMatch(query string, item RagContextItem) float64 {
	score := 0.0

	// Exact matches get highest score
	if strings.Contains(strings.ToLower(item.Title), query) {
		score += 0.8
	}
	if strings.Contains(strings.ToLower(item.Content), query) {
		score += 0.4
	}

	// Keyword matches
	for _, keyword := range item.Keywords {
		if strings.Contains(strings.ToLower(keyword), query) {
			score += 0.6
		}
	}

	// Metadata matches (for addresses, symbols, etc.)
	for _, value := range item.Metadata {
		if str, ok := value.(string); ok {
			if strings.Contains(strings.ToLower(str), query) {
				score += 0.7
			}
		}
	}

	// Partial matches (fuzzy)
	queryWords := strings.Fields(query)
	for _, word := range queryWords {
		if len(word) > 2 { // Skip very short words
			if strings.Contains(strings.ToLower(item.Title), word) {
				score += 0.2
			}
			if strings.Contains(strings.ToLower(item.Content), word) {
				score += 0.1
			}
		}
	}

	// Apply item's base relevance
	score *= item.Relevance

	return score
}

// extractDescription extracts a clean description from content
func (r *RAGSearchService) extractDescription(content string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Description:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Description:"))
		}
	}

	// Fallback: return first non-empty line that looks like a description
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) > 10 && !strings.Contains(line, ":") {
			return line
		}
	}

	return ""
}

// GetLangChainGoTools returns LangChainGo compatible tool definitions for function calling
func (r *RAGSearchService) GetLangChainGoTools() []llms.Tool {
	return []llms.Tool{
		{
			Type: "function",
			Function: &llms.FunctionDefinition{
				Name:        "search_protocols",
				Description: "Search for DeFi protocol information. Use this when you encounter unknown protocol names, contract addresses that might be protocols, or need to identify what protocol a transaction is interacting with. This does fuzzy matching so partial names work well.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "Search query - can be protocol name (like '1inch', 'uniswap'), contract address, or partial match. Examples: 'aggregator', '0x111111125421ca6dc', 'uni', 'compound'",
						},
					},
					"required": []string{"query"},
				},
			},
		},
		{
			Type: "function",
			Function: &llms.FunctionDefinition{
				Name:        "search_tokens",
				Description: "Search for token information. Use this when you encounter unknown token addresses, symbols, or need token metadata. This does fuzzy matching on addresses, symbols, and names.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"address_or_symbol": map[string]any{
							"type":        "string",
							"description": "Token search query - can be contract address, symbol, or name. Examples: '0xa0b86a33e6776848cc', 'USDT', 'chainlink', 'LINK'",
						},
					},
					"required": []string{"address_or_symbol"},
				},
			},
		},
		{
			Type: "function",
			Function: &llms.FunctionDefinition{
				Name:        "search_addresses",
				Description: "Search for well-known blockchain addresses. Use this when you encounter addresses that might be important contracts, multisigs, or known entities but aren't tokens or protocols.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"address": map[string]any{
							"type":        "string",
							"description": "Blockchain address to search for. Can be full address or partial. Examples: '0x000000000022d473030f116d', '0x22d4...ba3'",
						},
					},
					"required": []string{"address"},
				},
			},
		},
	}
}

// HandleFunctionCall processes LLM function calls and returns results
func (r *RAGSearchService) HandleFunctionCall(ctx context.Context, functionName string, arguments map[string]interface{}) (interface{}, error) {
	if r.verbose {
		fmt.Printf("RAG Function Call: %s with args %+v\n", functionName, arguments)
	}

	switch functionName {
	case "search_protocols":
		if query, ok := arguments["query"].(string); ok {
			return r.SearchProtocols(ctx, query)
		}
		return nil, fmt.Errorf("search_protocols requires 'query' parameter")

	case "search_tokens":
		if addressOrSymbol, ok := arguments["address_or_symbol"].(string); ok {
			return r.SearchTokens(ctx, addressOrSymbol)
		}
		return nil, fmt.Errorf("search_tokens requires 'address_or_symbol' parameter")

	case "search_addresses":
		if address, ok := arguments["address"].(string); ok {
			return r.SearchAddresses(ctx, address)
		}
		return nil, fmt.Errorf("search_addresses requires 'address' parameter")

	default:
		return nil, fmt.Errorf("unknown function: %s", functionName)
	}
}
