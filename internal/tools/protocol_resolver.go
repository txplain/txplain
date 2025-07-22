package tools

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/tmc/langchaingo/llms"
	"github.com/txplain/txplain/internal/models"
)

// ProtocolResolver identifies DeFi protocols probabilistically using AI and RAG
type ProtocolResolver struct {
	llm                 llms.Model
	verbose             bool
	confidenceThreshold float64 // Minimum confidence to include a protocol
	protocolKnowledge   []ProtocolKnowledge // RAG data from protocols.csv
}

// ProtocolKnowledge represents protocol information from CSV for RAG
type ProtocolKnowledge struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Website     string `json:"website"`
	Description string `json:"description"`
	IconURL     string `json:"icon_url"`
}

// ProbabilisticProtocol represents a protocol detection with confidence
type ProbabilisticProtocol struct {
	Name        string    `json:"name"`
	Type        string    `json:"type"`        // DEX, Aggregator, Lending, etc.
	Version     string    `json:"version"`     // v2, v3, etc.
	Confidence  float64   `json:"confidence"`  // 0.0 to 1.0
	Evidence    []string  `json:"evidence"`    // Reasons for this identification
	Contracts   []string  `json:"contracts"`   // Related contract addresses
	Website     string    `json:"website,omitempty"`
	Category    string    `json:"category,omitempty"` // DeFi, NFT, Gaming, etc.
}

// NewProtocolResolver creates a new probabilistic protocol resolver
func NewProtocolResolver(llm llms.Model) *ProtocolResolver {
	resolver := &ProtocolResolver{
		llm:                 llm,
		verbose:             false,
		confidenceThreshold: 0.6, // 60% minimum confidence
		protocolKnowledge:   []ProtocolKnowledge{},
	}
	
	// Load protocol knowledge from CSV for RAG
	if err := resolver.loadProtocolKnowledge(); err != nil {
		// Log error but don't fail - AI can still work without CSV data
		if resolver.verbose {
			fmt.Printf("Warning: Failed to load protocol knowledge from CSV: %v\n", err)
		}
	}
	
	return resolver
}

// SetVerbose enables or disables verbose logging
func (p *ProtocolResolver) SetVerbose(verbose bool) {
	p.verbose = verbose
}

// SetConfidenceThreshold sets minimum confidence required for protocol detection
func (p *ProtocolResolver) SetConfidenceThreshold(threshold float64) {
	p.confidenceThreshold = threshold
}

// Name returns the tool name
func (p *ProtocolResolver) Name() string {
	return "protocol_resolver"
}

// Description returns the tool description
func (p *ProtocolResolver) Description() string {
	return "Probabilistically identifies DeFi protocols using AI analysis and curated knowledge base"
}

// Dependencies returns the tools this processor depends on
func (p *ProtocolResolver) Dependencies() []string {
	return []string{"abi_resolver", "token_transfer_extractor", "token_metadata_enricher"}
}

// Process identifies protocols probabilistically from transaction data
func (p *ProtocolResolver) Process(ctx context.Context, baggage map[string]interface{}) error {
	// Gather transaction context for AI analysis
	contextData := p.buildTransactionContext(baggage)
	
	if p.verbose {
		fmt.Println("=== PROTOCOL RESOLVER: TRANSACTION CONTEXT ===")
		fmt.Printf("Context: %s\n", contextData)
		fmt.Println("=== END CONTEXT ===")
		fmt.Println()
	}

	// Use AI to identify protocols
	protocols, err := p.identifyProtocolsWithAI(ctx, contextData)
	if err != nil {
		if p.verbose {
			fmt.Printf("Protocol Resolution failed: %v\n", err)
		}
		// Fall back to empty list - don't fail the whole pipeline
		protocols = []ProbabilisticProtocol{}
	}

	// Filter by confidence threshold
	var highConfidenceProtocols []ProbabilisticProtocol
	for _, protocol := range protocols {
		if protocol.Confidence >= p.confidenceThreshold {
			highConfidenceProtocols = append(highConfidenceProtocols, protocol)
		}
	}

	if p.verbose {
		fmt.Printf("Protocol Resolver: Found %d protocols above %.1f%% confidence\n", 
			len(highConfidenceProtocols), p.confidenceThreshold*100)
		for i, protocol := range highConfidenceProtocols {
			fmt.Printf("  Protocol[%d]: %s (%s) - %.1f%% confidence\n", 
				i, protocol.Name, protocol.Type, protocol.Confidence*100)
		}
	}

	// Store results in baggage
	baggage["protocols"] = highConfidenceProtocols
	return nil
}

// loadProtocolKnowledge loads protocol data from protocols.csv for RAG context
func (p *ProtocolResolver) loadProtocolKnowledge() error {
	// Try multiple possible CSV locations
	csvPaths := []string{
		"data/protocols.csv",
		"./data/protocols.csv",
		"../data/protocols.csv",
	}
	
	var csvPath string
	var csvFile *os.File
	var err error
	
	// Find the CSV file
	for _, path := range csvPaths {
		if csvFile, err = os.Open(path); err == nil {
			csvPath = path
			break
		}
	}
	
	if csvFile == nil {
		return fmt.Errorf("could not find protocols.csv in any of the expected locations: %v", csvPaths)
	}
	defer csvFile.Close()
	
	if p.verbose {
		fmt.Printf("Loading protocol knowledge from: %s\n", csvPath)
	}
	
	reader := csv.NewReader(csvFile)
	records, err := reader.ReadAll()
	if err != nil {
		return fmt.Errorf("failed to read CSV: %w", err)
	}
	
	if len(records) == 0 {
		return fmt.Errorf("empty CSV file")
	}
	
	// Parse CSV (expecting header row)
	header := records[0]
	nameIndex := findColumnIndex(header, "name")
	websiteIndex := findColumnIndex(header, "website_url")
	descriptionIndex := findColumnIndex(header, "description")
	iconIndex := findColumnIndex(header, "icon_url")
	
	if nameIndex == -1 || websiteIndex == -1 || descriptionIndex == -1 {
		return fmt.Errorf("CSV missing required columns (name, website_url, description)")
	}
	
	// Parse data rows
	for _, record := range records[1:] {
		if len(record) <= nameIndex || len(record) <= websiteIndex || len(record) <= descriptionIndex {
			continue // Skip incomplete rows
		}
		
		knowledge := ProtocolKnowledge{
			Name:        strings.TrimSpace(record[nameIndex]),
			Website:     strings.TrimSpace(record[websiteIndex]),
			Description: strings.TrimSpace(record[descriptionIndex]),
		}
		
		// Infer type from description or name
		knowledge.Type = p.inferProtocolType(knowledge.Name, knowledge.Description)
		
		// Add icon URL if available
		if iconIndex != -1 && len(record) > iconIndex {
			knowledge.IconURL = strings.TrimSpace(record[iconIndex])
		}
		
		if knowledge.Name != "" {
			p.protocolKnowledge = append(p.protocolKnowledge, knowledge)
		}
	}
	
	if p.verbose {
		fmt.Printf("Loaded %d protocols from CSV for RAG context\n", len(p.protocolKnowledge))
	}
	
	return nil
}

// findColumnIndex finds the index of a column by name (case insensitive)
func findColumnIndex(headers []string, columnName string) int {
	for i, header := range headers {
		if strings.EqualFold(strings.TrimSpace(header), columnName) {
			return i
		}
	}
	return -1
}

// inferProtocolType attempts to infer protocol type from name and description
func (p *ProtocolResolver) inferProtocolType(name, description string) string {
	nameDesc := strings.ToLower(name + " " + description)
	
	if strings.Contains(nameDesc, "dex") || strings.Contains(nameDesc, "exchange") || 
	   strings.Contains(nameDesc, "swap") || strings.Contains(nameDesc, "liquidity") {
		return "DEX"
	}
	if strings.Contains(nameDesc, "aggregator") || strings.Contains(nameDesc, "routing") {
		return "Aggregator"
	}
	if strings.Contains(nameDesc, "lending") || strings.Contains(nameDesc, "borrow") {
		return "Lending"
	}
	if strings.Contains(nameDesc, "yield") || strings.Contains(nameDesc, "farm") {
		return "Yield"
	}
	if strings.Contains(nameDesc, "oracle") || strings.Contains(nameDesc, "price") {
		return "Oracle"
	}
	
	return "DeFi" // Default category
}

// buildTransactionContext creates context for AI analysis
func (p *ProtocolResolver) buildTransactionContext(baggage map[string]interface{}) string {
	var contextParts []string

	// Add contract addresses and their metadata
	if contractAddresses, ok := baggage["contract_addresses"].([]string); ok {
		contextParts = append(contextParts, "CONTRACT ADDRESSES:")
		for _, addr := range contractAddresses {
			contextParts = append(contextParts, fmt.Sprintf("- %s", addr))
		}
	}

	// Add token transfers context
	if transfers, ok := baggage["transfers"].([]models.TokenTransfer); ok && len(transfers) > 0 {
		contextParts = append(contextParts, "\nTOKEN TRANSFERS:")
		for i, transfer := range transfers {
			transferInfo := fmt.Sprintf("Transfer #%d: %s -> %s", i+1, transfer.From, transfer.To)
			if transfer.Symbol != "" {
				transferInfo += fmt.Sprintf(" (%s)", transfer.Symbol)
			}
			if transfer.FormattedAmount != "" {
				transferInfo += fmt.Sprintf(" Amount: %s", transfer.FormattedAmount)
			}
			contextParts = append(contextParts, fmt.Sprintf("- %s", transferInfo))
		}
	}

	// Add token metadata context
	if tokenMetadata, ok := baggage["token_metadata"].(map[string]*TokenMetadata); ok {
		contextParts = append(contextParts, "\nTOKEN METADATA:")
		for addr, metadata := range tokenMetadata {
			metaInfo := fmt.Sprintf("%s: %s (%s)", addr, metadata.Name, metadata.Symbol)
			if metadata.Type != "" {
				metaInfo += fmt.Sprintf(" [%s]", metadata.Type)
			}
			contextParts = append(contextParts, fmt.Sprintf("- %s", metaInfo))
		}
	}

	// Add events context
	if events, ok := baggage["events"].([]models.Event); ok && len(events) > 0 {
		contextParts = append(contextParts, "\nEVENTS:")
		for _, event := range events {
			eventInfo := fmt.Sprintf("%s on %s", event.Name, event.Contract)
			contextParts = append(contextParts, fmt.Sprintf("- %s", eventInfo))
		}
	}

	// Add raw transaction context
	if rawData, ok := baggage["raw_data"].(map[string]interface{}); ok {
		if receipt, ok := rawData["receipt"].(map[string]interface{}); ok {
			if to, ok := receipt["to"].(string); ok {
				contextParts = append(contextParts, fmt.Sprintf("\nTRANSACTION TO: %s", to))
			}
		}
	}

	return strings.Join(contextParts, "\n")
}

// identifyProtocolsWithAI uses LLM to identify protocols from context
func (p *ProtocolResolver) identifyProtocolsWithAI(ctx context.Context, contextData string) ([]ProbabilisticProtocol, error) {
	prompt := p.buildProtocolAnalysisPrompt(contextData)

	if p.verbose {
		fmt.Println("=== PROTOCOL RESOLVER: PROMPT ===")
		fmt.Println(prompt)
		fmt.Println("=== END PROMPT ===")
		fmt.Println()
	}

	// Call LLM
	response, err := p.llm.GenerateContent(ctx, []llms.MessageContent{
		{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{
				llms.TextPart(prompt),
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("LLM call failed: %w", err)
	}

	responseText := ""
	if response != nil && len(response.Choices) > 0 {
		responseText = response.Choices[0].Content
	}

	if p.verbose {
		fmt.Println("=== PROTOCOL RESOLVER: LLM RESPONSE ===")
		fmt.Println(responseText)
		fmt.Println("=== END RESPONSE ===")
		fmt.Println()
	}

	// Parse AI response
	protocols, err := p.parseProtocolResponse(responseText)
	if err != nil {
		return nil, fmt.Errorf("failed to parse protocol response: %w", err)
	}

	return protocols, nil
}

// buildProtocolAnalysisPrompt creates the prompt for AI protocol identification
func (p *ProtocolResolver) buildProtocolAnalysisPrompt(contextData string) string {
	// Build RAG context from CSV knowledge
	var knowledgeContext strings.Builder
	knowledgeContext.WriteString("CURATED PROTOCOL KNOWLEDGE (use as reference):\n")
	
	for _, knowledge := range p.protocolKnowledge {
		knowledgeContext.WriteString(fmt.Sprintf("- %s (%s): %s [Website: %s]\n", 
			knowledge.Name, knowledge.Type, knowledge.Description, knowledge.Website))
	}

	prompt := fmt.Sprintf(`You are an expert DeFi protocol analyst. Analyze the following blockchain transaction context and identify which DeFi protocols are likely involved.

%s

TRANSACTION CONTEXT:
%s

Your task is to probabilistically identify protocols based on the transaction patterns, contract addresses, token transfers, and events. Use the curated knowledge above as reference but also apply your knowledge of other protocols.

IDENTIFICATION PATTERNS:
- Contract addresses (direct matches or similar patterns)
- Token symbols and names (protocol-specific tokens)
- Event signatures (Transfer, Swap, Mint, Burn patterns)
- Transfer patterns (multi-hop swaps, liquidity provision)
- Token combinations (DAI+MKR suggests MakerDAO, cTokens suggest Compound)

ANALYSIS CRITERIA:
- Direct contract matches = HIGH confidence (0.9-1.0)
- Strong pattern matches = MEDIUM-HIGH confidence (0.7-0.9)
- Probable patterns = MEDIUM confidence (0.5-0.7)
- Weak indicators = LOW confidence (0.3-0.5)
- Speculation only = VERY LOW confidence (0.0-0.3)

OUTPUT FORMAT:
Respond with a JSON array of protocol identifications. Each should include:
{
  "name": "Protocol Name",
  "type": "DEX|Aggregator|Lending|Oracle|CDP|Yield|Bridge|NFT|Gaming",
  "version": "v2|v3|etc or empty",
  "confidence": 0.85,
  "evidence": ["reason 1", "reason 2"],
  "contracts": ["0x..."],
  "website": "https://...",
  "category": "DeFi|NFT|Gaming|Infrastructure"
}

EXAMPLES:
[
  {
    "name": "Uniswap",
    "type": "DEX",
    "version": "v2",
    "confidence": 0.95,
    "evidence": ["Direct contract match 0x7a250d56...", "Swap event pattern", "WETH/ERC20 pair"],
    "contracts": ["0x7a250d5630b4cf539739df2c5dacb4c659f2488d"],
    "website": "https://uniswap.org",
    "category": "DeFi"
  },
  {
    "name": "1inch",
    "type": "Aggregator",
    "version": "v6",
    "confidence": 0.87,
    "evidence": ["1inch v6 aggregator contract", "Multi-hop swap pattern", "Gas optimization"],
    "contracts": ["0x111111125421ca6dc452d289314280a0f8842a65"],
    "website": "https://1inch.io",
    "category": "DeFi"
  }
]

Analyze the transaction context and return only protocols you can identify with reasonable confidence (> 0.3). Be conservative - it's better to miss a protocol than to incorrectly identify one.`, knowledgeContext.String(), contextData)

	return prompt
}

// parseProtocolResponse parses the AI response into protocol structures
func (p *ProtocolResolver) parseProtocolResponse(response string) ([]ProbabilisticProtocol, error) {
	// Clean up response - extract JSON
	response = strings.TrimSpace(response)
	
	// Look for JSON array
	jsonStart := strings.Index(response, "[")
	jsonEnd := strings.LastIndex(response, "]")
	
	if jsonStart == -1 || jsonEnd == -1 || jsonEnd <= jsonStart {
		return nil, fmt.Errorf("no valid JSON array found in response")
	}
	
	jsonStr := response[jsonStart : jsonEnd+1]
	
	// Parse JSON
	var protocols []ProbabilisticProtocol
	if err := json.Unmarshal([]byte(jsonStr), &protocols); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}
	
	// Validate and clean up protocols
	var validProtocols []ProbabilisticProtocol
	for _, protocol := range protocols {
		// Validate required fields
		if protocol.Name == "" || protocol.Type == "" {
			continue
		}
		
		// Ensure confidence is in valid range
		if protocol.Confidence < 0 {
			protocol.Confidence = 0
		}
		if protocol.Confidence > 1 {
			protocol.Confidence = 1
		}
		
		// Clean up fields
		protocol.Name = strings.TrimSpace(protocol.Name)
		protocol.Type = strings.TrimSpace(protocol.Type)
		protocol.Version = strings.TrimSpace(protocol.Version)
		protocol.Category = strings.TrimSpace(protocol.Category)
		
		validProtocols = append(validProtocols, protocol)
	}
	
	return validProtocols, nil
}

// GetPromptContext provides protocol context for other AI tools
func (p *ProtocolResolver) GetPromptContext(ctx context.Context, baggage map[string]interface{}) string {
	protocols, ok := baggage["protocols"].([]ProbabilisticProtocol)
	if !ok || len(protocols) == 0 {
		return ""
	}

	var contextParts []string
	contextParts = append(contextParts, "### Detected Protocols:")

	for i, protocol := range protocols {
		protocolInfo := fmt.Sprintf("\nProtocol #%d:", i+1)
		protocolInfo += fmt.Sprintf("\n- Name: %s (%s)", protocol.Name, protocol.Type)
		if protocol.Version != "" {
			protocolInfo += fmt.Sprintf(" %s", protocol.Version)
		}
		protocolInfo += fmt.Sprintf("\n- Confidence: %.1f%%", protocol.Confidence*100)
		
		if len(protocol.Evidence) > 0 {
			protocolInfo += "\n- Evidence:"
			for _, evidence := range protocol.Evidence {
				protocolInfo += fmt.Sprintf("\n  • %s", evidence)
			}
		}
		
		if protocol.Website != "" {
			protocolInfo += fmt.Sprintf("\n- Website: %s", protocol.Website)
		}
		
		contextParts = append(contextParts, protocolInfo)
	}

	contextParts = append(contextParts, fmt.Sprintf("\n\nNote: Protocols identified with %.1f%% minimum confidence threshold", p.confidenceThreshold*100))

	return strings.Join(contextParts, "")
}

// GetAnnotationContext provides annotation context for protocols
func (p *ProtocolResolver) GetAnnotationContext(ctx context.Context, baggage map[string]interface{}) *models.AnnotationContext {
	protocols, ok := baggage["protocols"].([]ProbabilisticProtocol)
	if !ok || len(protocols) == 0 {
		return &models.AnnotationContext{Items: make([]models.AnnotationContextItem, 0)}
	}

	annotationContext := &models.AnnotationContext{
		Items: make([]models.AnnotationContextItem, 0),
	}

	for _, protocol := range protocols {
		description := fmt.Sprintf("%s protocol (%.1f%% confidence)", protocol.Type, protocol.Confidence*100)
		if protocol.Category != "" {
			description += fmt.Sprintf(" in %s category", protocol.Category)
		}

		item := models.AnnotationContextItem{
			Type:        "protocol",
			Value:       protocol.Name,
			Name:        fmt.Sprintf("%s %s", protocol.Name, protocol.Version),
			Link:        protocol.Website,
			Description: description,
			Metadata: map[string]interface{}{
				"type":       protocol.Type,
				"version":    protocol.Version,
				"confidence": protocol.Confidence,
				"evidence":   protocol.Evidence,
				"category":   protocol.Category,
			},
		}

		annotationContext.AddItem(item)

		// Also add version-specific variants for better matching
		if protocol.Version != "" {
			versionItem := item
			versionItem.Value = fmt.Sprintf("%s %s", protocol.Name, protocol.Version)
			versionItem.Name = fmt.Sprintf("%s %s", protocol.Name, protocol.Version)
			annotationContext.AddItem(versionItem)
		}
	}

	return annotationContext
}

