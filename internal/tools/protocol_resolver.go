package tools

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

// ProtocolResolver identifies DeFi protocols probabilistically using AI and RAG
type ProtocolResolver struct {
	llm                 llms.Model
	verbose             bool
	confidenceThreshold float64             // Minimum confidence to include a protocol
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
	Name       string   `json:"name"`
	Type       string   `json:"type"`       // DEX, Aggregator, Lending, etc.
	Version    string   `json:"version"`    // v2, v3, etc.
	Confidence float64  `json:"confidence"` // 0.0 to 1.0
	Evidence   []string `json:"evidence"`   // Reasons for this identification
	Contracts  []string `json:"contracts"`  // Related contract addresses
	Website    string   `json:"website,omitempty"`
	Category   string   `json:"category,omitempty"` // DeFi, NFT, Gaming, etc.
}

// NewProtocolResolver creates a new probabilistic protocol resolver
func NewProtocolResolver(llm llms.Model, verbose bool, confidenceThreshold float64) *ProtocolResolver {
	resolver := &ProtocolResolver{
		llm:                 llm,
		verbose:             verbose,
		confidenceThreshold: confidenceThreshold,
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
	if p.verbose {
		fmt.Println("\n" + strings.Repeat("🏛️", 60))
		fmt.Printf("🔍 PROTOCOL RESOLVER: Starting AI-powered protocol detection (threshold: %.1f%%)\n", p.confidenceThreshold*100)
		fmt.Printf("📚 Knowledge base: %d protocols loaded\n", len(p.protocolKnowledge))
		fmt.Println(strings.Repeat("🏛️", 60))
	}

	// Collect context from all context providers in the baggage (same as TransactionExplainer)
	var additionalContext []string
	if contextProviders, ok := baggage["context_providers"].([]interface{}); ok {
		for _, provider := range contextProviders {
			if toolProvider, ok := provider.(Tool); ok {
				if context := toolProvider.GetPromptContext(ctx, baggage); context != "" {
					additionalContext = append(additionalContext, context)
				}
			}
		}
	}

	// Combine all context for AI analysis
	contextData := strings.Join(additionalContext, "\n\n")

	if p.verbose {
		fmt.Printf("📊 Built analysis context: %d characters\n", len(contextData))
	}

	// Use AI to identify protocols with context from previous tools
	protocols, err := p.identifyProtocolsWithAI(ctx, contextData)
	if err != nil {
		if p.verbose {
			fmt.Printf("❌ AI protocol detection failed: %v\n", err)
			fmt.Println("⚠️  Falling back to empty protocol list")
		}
		// Fall back to empty list - don't fail the whole pipeline
		protocols = []ProbabilisticProtocol{}
	}

	if p.verbose && len(protocols) > 0 {
		fmt.Printf("🧠 AI detected %d potential protocols\n", len(protocols))
	}

	// Filter by confidence threshold
	var highConfidenceProtocols []ProbabilisticProtocol
	for _, protocol := range protocols {
		if protocol.Confidence >= p.confidenceThreshold {
			highConfidenceProtocols = append(highConfidenceProtocols, protocol)
		}
	}

	if p.verbose {
		fmt.Printf("✅ Filtered to %d protocols above %.1f%% confidence\n",
			len(highConfidenceProtocols), p.confidenceThreshold*100)

		// Show summary of detected protocols
		if len(highConfidenceProtocols) > 0 {
			fmt.Println("\n📋 DETECTED PROTOCOLS SUMMARY:")
			for i, protocol := range highConfidenceProtocols {
				fmt.Printf("   %d. %s (%s %s) - Confidence: %.1f%%\n",
					i+1, protocol.Name, protocol.Type, protocol.Version, protocol.Confidence*100)
			}
		}

		fmt.Println("\n" + strings.Repeat("🏛️", 60))
		fmt.Println("✅ PROTOCOL RESOLVER: Completed successfully")
		fmt.Println(strings.Repeat("🏛️", 60) + "\n")
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
			Type:        "DeFi", // Generic default - let LLM classify based on description
		}

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

// identifyProtocolsWithAI uses AI to identify protocols from transaction context
func (p *ProtocolResolver) identifyProtocolsWithAI(ctx context.Context, contextData string) ([]ProbabilisticProtocol, error) {
	prompt := p.buildProtocolAnalysisPrompt(contextData)

	if p.verbose {
		fmt.Println("\n" + strings.Repeat("=", 80))
		fmt.Println("🤖 PROTOCOL RESOLVER: LLM PROMPT")
		fmt.Println(strings.Repeat("=", 80))
		fmt.Println(prompt)
		fmt.Println(strings.Repeat("=", 80))
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
		fmt.Println(strings.Repeat("=", 80))
		fmt.Println("🤖 PROTOCOL RESOLVER: LLM RESPONSE")
		fmt.Println(strings.Repeat("=", 80))
		fmt.Println(responseText)
		fmt.Println(strings.Repeat("=", 80) + "\n")
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

CRITICAL DISTINCTION - TOKEN CONTRACTS vs PROTOCOL CONTRACTS:
- VERIFIED CONTRACT NAMES are the most authoritative source of information
- Contract names like "AggregationRouterV6", "UniswapV2Router02", "1inchRouter" indicate PROTOCOL contracts
- Contract names like "TetherToken", "USD Coin", "Wrapped Ether" indicate TOKEN contracts
- If a contract has a verified name, use that name for classification, NOT method signatures
- Method signatures (name, symbol, decimals) are secondary indicators - many protocol contracts implement these for compatibility
- Approval events happen ON token contracts but are FOR protocol contracts (the spender)

IDENTIFICATION PRIORITY (in order):
1. VERIFIED CONTRACT NAMES - Use Etherscan-verified contract names as primary classification source
2. TRANSACTION TO field - The actual protocol contract being called
3. SPENDER addresses in Approval events - Potential protocol contracts being approved
4. Method signatures and interfaces - Secondary indicators only
5. Token symbols and transfers - Context about what's being traded, not protocol identification

ANALYSIS CRITERIA:
- Direct contract matches to KNOWN protocol addresses = HIGH confidence (0.9-1.0)
- Strong patterns with verified protocol contracts = MEDIUM-HIGH confidence (0.7-0.9)
- Likely protocol usage but unclear which contract = MEDIUM confidence (0.5-0.7)
- Weak indicators or token-only evidence = LOW confidence (0.3-0.5)
- Pure speculation = VERY LOW confidence (0.0-0.3)

CONSERVATIVE APPROACH:
- It's better to NOT identify a protocol than to incorrectly identify one
- ALWAYS prioritize verified contract names over method signature patterns
- Don't classify contracts as tokens just because they implement name(), symbol(), decimals()
- Router contracts like "AggregationRouterV6" that implement token methods are still ROUTERS, not tokens
- If you only see method signatures without verified names, be very cautious about classification
- Require actual evidence of protocol contracts, preferably with verified names

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

EXAMPLES OF CORRECT ANALYSIS:

Good Example 1 - Using Verified Contract Name:
[
  {
    "name": "1inch Network",
    "type": "Aggregator",
    "version": "v6",
    "confidence": 0.95,
    "evidence": ["Contract 0x111111125421ca6dc452d289314280a0f8842a65 has verified name 'AggregationRouterV6'", "Verified contract indicates 1inch aggregator protocol"],
    "contracts": ["0x111111125421ca6dc452d289314280a0f8842a65"],
    "website": "https://1inch.io",
    "category": "DeFi"
  }
]

Good Example 2 - Using Transaction TO field:
[
  {
    "name": "Uniswap",
    "type": "DEX",
    "version": "v2",
    "confidence": 0.9,
    "evidence": ["Transaction directly calls 0x7a250d5630b4cf539739df2c5dacb4c659f2488d", "Known Uniswap V2 Router address", "Swap-related transaction pattern"],
    "contracts": ["0x7a250d5630b4cf539739df2c5dacb4c659f2488d"],
    "website": "https://uniswap.org",
    "category": "DeFi"
  }
]

Good Example 3 - Conservative Unknown Router:
[
  {
    "name": "Unknown DEX/Aggregator",
    "type": "Aggregator",
    "version": "",
    "confidence": 0.4,
    "evidence": ["Token approvals suggest DeFi interaction", "No verified contract name available", "Router-like transaction pattern"],
    "contracts": [],
    "website": "",
    "category": "DeFi"
  }
]

Bad Example - DON'T DO THIS:
[
  {
    "name": "1inch Network",
    "type": "Token", 
    "confidence": 0.8,
    "evidence": ["Contract has name() and symbol() methods"],  // ❌ WRONG: Ignoring verified name "AggregationRouterV6"
    "contracts": ["0x111111125421ca6dc452d289314280a0f8842a65"]  // ❌ WRONG: This is a router, not a token
  }
]

Analyze the transaction context and return only protocols you can identify with reasonable confidence (> 0.3). 

PRIORITY: Use verified contract names as your PRIMARY classification source. If a contract has a verified name like "AggregationRouterV6" or "UniswapV2Router02", that is definitive evidence of the contract's purpose, regardless of what methods it implements. Method signatures like name(), symbol(), decimals() are compatibility features and do not determine contract classification.`,
		knowledgeContext.String(),
		contextData)

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

// GetRagContext provides RAG context for protocol information
func (p *ProtocolResolver) GetRagContext(ctx context.Context, baggage map[string]interface{}) *RagContext {
	ragContext := NewRagContext()

	protocols, ok := baggage["protocols"].([]ProbabilisticProtocol)
	if !ok || len(protocols) == 0 {
		return ragContext
	}

	// Add protocol information to RAG context for searchability
	for _, protocol := range protocols {
		if protocol.Confidence >= p.confidenceThreshold {
			ragContext.AddItem(RagContextItem{
				ID:      fmt.Sprintf("protocol_%s", strings.ReplaceAll(strings.ToLower(protocol.Name), " ", "_")),
				Type:    "protocol",
				Title:   fmt.Sprintf("%s Protocol", protocol.Name),
				Content: fmt.Sprintf("Protocol %s is a %s %s of type %s with confidence %.2f", protocol.Name, protocol.Type, protocol.Version, protocol.Category, protocol.Confidence),
				Metadata: map[string]interface{}{
					"name":       protocol.Name,
					"type":       protocol.Type,
					"version":    protocol.Version,
					"category":   protocol.Category,
					"confidence": protocol.Confidence,
				},
				Keywords:  []string{protocol.Name, protocol.Type, protocol.Category, "protocol"},
				Relevance: float64(protocol.Confidence),
			})
		}
	}

	return ragContext
}
