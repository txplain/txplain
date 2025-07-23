package tools

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/tmc/langchaingo/llms"
	"github.com/txplain/txplain/internal/models"
)

// TagResolver identifies transaction tags probabilistically using AI and RAG
type TagResolver struct {
	llm                 llms.Model
	verbose             bool
	confidenceThreshold float64        // Minimum confidence to include a tag
	tagKnowledge        []TagKnowledge // RAG data from tags.csv
}

// TagKnowledge represents tag information from CSV for RAG
type TagKnowledge struct {
	Tag              string  `json:"tag"`
	Category         string  `json:"category"`
	Description      string  `json:"description"`
	Patterns         string  `json:"patterns"`
	ConfidenceWeight float64 `json:"confidence_weight"`
}

// ProbabilisticTag represents a tag detection with confidence
type ProbabilisticTag struct {
	Tag         string   `json:"tag"`
	Category    string   `json:"category"`
	Confidence  float64  `json:"confidence"` // 0.0 to 1.0
	Evidence    []string `json:"evidence"`   // Reasons for this identification
	Description string   `json:"description,omitempty"`
}

// NewTagResolver creates a new probabilistic tag resolver
func NewTagResolver(llm llms.Model) *TagResolver {
	resolver := &TagResolver{
		llm:                 llm,
		verbose:             false,
		confidenceThreshold: 0.6, // 60% minimum confidence
		tagKnowledge:        []TagKnowledge{},
	}

	// Load tag knowledge from CSV for RAG
	if err := resolver.loadTagKnowledge(); err != nil {
		// Log error but don't fail - AI can still work without CSV data
		if resolver.verbose {
			fmt.Printf("Warning: Failed to load tag knowledge from CSV: %v\n", err)
		}
	}

	return resolver
}

// SetVerbose enables or disables verbose logging
func (t *TagResolver) SetVerbose(verbose bool) {
	t.verbose = verbose
}

// SetConfidenceThreshold sets minimum confidence required for tag detection
func (t *TagResolver) SetConfidenceThreshold(threshold float64) {
	t.confidenceThreshold = threshold
}

// Name returns the tool name
func (t *TagResolver) Name() string {
	return "tag_resolver"
}

// Description returns the tool description
func (t *TagResolver) Description() string {
	return "Probabilistically identifies transaction tags using AI analysis and curated knowledge base"
}

// Dependencies returns the tools this processor depends on
func (t *TagResolver) Dependencies() []string {
	return []string{"abi_resolver", "log_decoder", "token_transfer_extractor", "protocol_resolver"}
}

// Process identifies tags probabilistically from transaction data
func (t *TagResolver) Process(ctx context.Context, baggage map[string]interface{}) error {
	// Gather transaction context for AI analysis
	contextData := t.buildTransactionContext(baggage)

	if t.verbose {
		fmt.Println("=== TAG RESOLVER: TRANSACTION CONTEXT ===")
		fmt.Printf("Context: %s\n", contextData)
		fmt.Println("=== END CONTEXT ===")
		fmt.Println()
	}

	// Use AI to identify tags
	tags, err := t.identifyTagsWithAI(ctx, contextData)
	if err != nil {
		if t.verbose {
			fmt.Printf("Tag Resolution failed: %v\n", err)
		}
		// Fall back to empty list - don't fail the whole pipeline
		tags = []ProbabilisticTag{}
	}

	// Filter by confidence threshold
	var highConfidenceTags []ProbabilisticTag
	for _, tag := range tags {
		if tag.Confidence >= t.confidenceThreshold {
			highConfidenceTags = append(highConfidenceTags, tag)
		}
	}

	if t.verbose {
		fmt.Printf("Tag Resolver: Found %d tags above %.1f%% confidence\n",
			len(highConfidenceTags), t.confidenceThreshold*100)
		for i, tag := range highConfidenceTags {
			fmt.Printf("  Tag[%d]: %s (%s) - %.1f%% confidence\n",
				i, tag.Tag, tag.Category, tag.Confidence*100)
		}
	}

	// Convert to string slice for backward compatibility
	var tagStrings []string
	for _, tag := range highConfidenceTags {
		tagStrings = append(tagStrings, tag.Tag)
	}

	// Store results in baggage
	baggage["tags"] = tagStrings                       // For backward compatibility
	baggage["probabilistic_tags"] = highConfidenceTags // Detailed information
	return nil
}

// loadTagKnowledge loads tag data from tags.csv for RAG context
func (t *TagResolver) loadTagKnowledge() error {
	// Try multiple possible CSV locations
	csvPaths := []string{
		"data/tags.csv",
		"./data/tags.csv",
		"../data/tags.csv",
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
		return fmt.Errorf("could not find tags.csv in any of the expected locations: %v", csvPaths)
	}
	defer csvFile.Close()

	if t.verbose {
		fmt.Printf("Loading tag knowledge from: %s\n", csvPath)
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
	tagIndex := findColumnIndex(header, "tag")
	categoryIndex := findColumnIndex(header, "category")
	descriptionIndex := findColumnIndex(header, "description")
	patternsIndex := findColumnIndex(header, "patterns")
	confidenceIndex := findColumnIndex(header, "confidence_weight")

	if tagIndex == -1 || categoryIndex == -1 || descriptionIndex == -1 || patternsIndex == -1 {
		return fmt.Errorf("CSV missing required columns (tag, category, description, patterns)")
	}

	// Parse data rows
	for _, record := range records[1:] {
		if len(record) <= tagIndex || len(record) <= categoryIndex || len(record) <= descriptionIndex || len(record) <= patternsIndex {
			continue // Skip incomplete rows
		}

		knowledge := TagKnowledge{
			Tag:              strings.TrimSpace(record[tagIndex]),
			Category:         strings.TrimSpace(record[categoryIndex]),
			Description:      strings.TrimSpace(record[descriptionIndex]),
			Patterns:         strings.TrimSpace(record[patternsIndex]),
			ConfidenceWeight: 0.8, // Default weight
		}

		// Parse confidence weight if available
		if confidenceIndex != -1 && len(record) > confidenceIndex {
			if weight, err := strconv.ParseFloat(strings.TrimSpace(record[confidenceIndex]), 64); err == nil {
				knowledge.ConfidenceWeight = weight
			}
		}

		if knowledge.Tag != "" {
			t.tagKnowledge = append(t.tagKnowledge, knowledge)
		}
	}

	if t.verbose {
		fmt.Printf("Loaded %d tags from CSV for RAG context\n", len(t.tagKnowledge))
	}

	return nil
}

// buildTransactionContext creates context for AI analysis
func (t *TagResolver) buildTransactionContext(baggage map[string]interface{}) string {
	var contextParts []string

	// Add protocol information
	if protocols, ok := baggage["protocols"].([]ProbabilisticProtocol); ok && len(protocols) > 0 {
		contextParts = append(contextParts, "DETECTED PROTOCOLS:")
		for _, protocol := range protocols {
			protocolInfo := fmt.Sprintf("- %s (%s)", protocol.Name, protocol.Type)
			if protocol.Version != "" {
				protocolInfo += fmt.Sprintf(" %s", protocol.Version)
			}
			contextParts = append(contextParts, protocolInfo)
		}
	}

	// Add contract addresses and their metadata
	if contractAddresses, ok := baggage["contract_addresses"].([]string); ok {
		contextParts = append(contextParts, "\nCONTRACT ADDRESSES:")
		for _, addr := range contractAddresses {
			contextParts = append(contextParts, fmt.Sprintf("- %s", addr))
		}
	}

	// Add events context (critical for tag detection)
	if events, ok := baggage["events"].([]models.Event); ok && len(events) > 0 {
		contextParts = append(contextParts, "\nEVENTS:")
		for _, event := range events {
			eventInfo := fmt.Sprintf("- %s on %s", event.Name, event.Contract)
			if event.Parameters != nil {
				// Add ALL event parameters generically - no hardcoded filtering
				var paramDetails []string

				// Include ALL parameters from the event - let LLM decide what's meaningful
				for paramName, paramValue := range event.Parameters {
					paramDetails = append(paramDetails, fmt.Sprintf("%s: %v", paramName, paramValue))
				}

				if len(paramDetails) > 0 {
					eventInfo += fmt.Sprintf(" (%s)", strings.Join(paramDetails, ", "))
				}
			}
			contextParts = append(contextParts, eventInfo)
		}
	}

	// Add token transfers context
	if transfers, ok := baggage["transfers"].([]models.TokenTransfer); ok && len(transfers) > 0 {
		contextParts = append(contextParts, "\nTOKEN TRANSFERS:")
		for i, transfer := range transfers {
			transferInfo := fmt.Sprintf("- Transfer #%d: %s -> %s", i+1, transfer.From, transfer.To)
			if transfer.Symbol != "" {
				transferInfo += fmt.Sprintf(" (%s)", transfer.Symbol)
			}
			if transfer.FormattedAmount != "" {
				transferInfo += fmt.Sprintf(" Amount: %s", transfer.FormattedAmount)
			}
			if transfer.Type != "" {
				transferInfo += fmt.Sprintf(" [%s]", transfer.Type)
			}
			contextParts = append(contextParts, transferInfo)
		}
	}

	// Add decoded calls context
	if decodedData, ok := baggage["decoded_data"].(*models.DecodedData); ok && len(decodedData.Calls) > 0 {
		contextParts = append(contextParts, "\nMETHOD CALLS:")
		for _, call := range decodedData.Calls {
			callInfo := fmt.Sprintf("- %s on %s", call.Method, call.Contract)
			if call.CallType != "" {
				callInfo += fmt.Sprintf(" (%s)", call.CallType)
			}
			if call.Value != "" && call.Value != "0" {
				callInfo += fmt.Sprintf(" with %s ETH", call.Value)
			}
			contextParts = append(contextParts, callInfo)
		}
	}

	// Add token metadata context
	if tokenMetadata, ok := baggage["token_metadata"].(map[string]*TokenMetadata); ok {
		contextParts = append(contextParts, "\nTOKEN METADATA:")
		for addr, metadata := range tokenMetadata {
			metaInfo := fmt.Sprintf("- %s: %s (%s)", addr, metadata.Name, metadata.Symbol)
			if metadata.Type != "" {
				metaInfo += fmt.Sprintf(" [%s]", metadata.Type)
			}
			contextParts = append(contextParts, metaInfo)
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

// identifyTagsWithAI uses LLM to identify tags from context
func (t *TagResolver) identifyTagsWithAI(ctx context.Context, contextData string) ([]ProbabilisticTag, error) {
	prompt := t.buildTagAnalysisPrompt(contextData)

	if t.verbose {
		fmt.Println("=== TAG RESOLVER: PROMPT ===")
		fmt.Println(prompt)
		fmt.Println("=== END PROMPT ===")
		fmt.Println()
	}

	// Call LLM
	response, err := t.llm.GenerateContent(ctx, []llms.MessageContent{
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

	if t.verbose {
		fmt.Println("=== TAG RESOLVER: LLM RESPONSE ===")
		fmt.Println(responseText)
		fmt.Println("=== END RESPONSE ===")
		fmt.Println()
	}

	// Parse AI response
	tags, err := t.parseTagResponse(responseText)
	if err != nil {
		return nil, fmt.Errorf("failed to parse tag response: %w", err)
	}

	return tags, nil
}

// buildTagAnalysisPrompt creates the prompt for AI tag identification
func (t *TagResolver) buildTagAnalysisPrompt(contextData string) string {
	// Build RAG context from CSV knowledge
	var knowledgeContext strings.Builder
	knowledgeContext.WriteString("CURATED TAG KNOWLEDGE (use as reference):\n\n")

	// Group by category for better organization
	categories := make(map[string][]TagKnowledge)
	for _, knowledge := range t.tagKnowledge {
		categories[knowledge.Category] = append(categories[knowledge.Category], knowledge)
	}

	for category, tags := range categories {
		knowledgeContext.WriteString(fmt.Sprintf("%s:\n", category))
		for _, tag := range tags {
			knowledgeContext.WriteString(fmt.Sprintf("- %s: %s (patterns: %s) [weight: %.2f]\n",
				tag.Tag, tag.Description, tag.Patterns, tag.ConfidenceWeight))
		}
		knowledgeContext.WriteString("\n")
	}

	prompt := fmt.Sprintf(`You are an expert blockchain transaction analyst. Analyze the following transaction context and identify appropriate tags that categorize this transaction.

%s

TRANSACTION CONTEXT:
%s

Your task is to probabilistically identify transaction tags based on the transaction patterns, protocols involved, events emitted, method calls, and token transfers. Use the curated tag knowledge above as reference but also apply your broader knowledge.

IDENTIFICATION PATTERNS:
- Method calls (transfer, approve, swap, mint, stake, etc.)
- Events emitted (Transfer, Swap, Approval, Mint, Burn, etc.)
- Protocols involved (DEX, lending, NFT marketplace, etc.)
- Token types (ERC20, ERC721, ERC1155, stablecoins, etc.)
- Transaction patterns (multi-hop swaps, liquidity provision, flash loans, etc.)
- Value flows (from/to addresses, amounts, fees)

CONFIDENCE CRITERIA:
- Explicit method/event matches = HIGH confidence (0.9-1.0)
- Strong protocol/pattern evidence = MEDIUM-HIGH confidence (0.7-0.9) 
- Probable indicators = MEDIUM confidence (0.5-0.7)
- Weak signals = LOW confidence (0.3-0.5)
- Speculation = VERY LOW confidence (0.0-0.3)

TAG SELECTION GUIDELINES:
- Be specific rather than generic (prefer "swap" over "defi" when applicable)
- Include both protocol-specific and action-specific tags
- Consider the primary action and secondary effects
- Include technical standards when relevant (erc20, erc721, etc.)
- Add infrastructure tags for technical aspects (batch, proxy, multisig)

OUTPUT FORMAT:
Respond with a JSON array of tag identifications. Each should include:
{
  "tag": "tag-name",
  "category": "Category",
  "confidence": 0.85,
  "evidence": ["reason 1", "reason 2"],
  "description": "What this tag represents in this context"
}

EXAMPLES:
[
  {
    "tag": "swap",
    "category": "DeFi",
    "confidence": 0.95,
    "evidence": ["Swap event emitted", "Uniswap protocol detected", "Token A->B exchange pattern"],
    "description": "Token swapping operation on a DEX"
  },
  {
    "tag": "erc20",
    "category": "Token",
    "confidence": 0.9,
    "evidence": ["Transfer events", "ERC20 tokens involved", "approve/transfer methods"],
    "description": "ERC20 token standard operations"
  },
  {
    "tag": "liquidity",
    "category": "DeFi", 
    "confidence": 0.8,
    "evidence": ["addLiquidity method call", "LP token minting", "Pair contract interaction"],
    "description": "Liquidity provision to AMM pool"
  }
]

Analyze the transaction context and return tags with reasonable confidence (> 0.3). Focus on accuracy - it's better to miss a tag than to incorrectly identify one. Limit to 8-10 most relevant tags.`, knowledgeContext.String(), contextData)

	return prompt
}

// parseTagResponse parses the AI response into tag structures
func (t *TagResolver) parseTagResponse(response string) ([]ProbabilisticTag, error) {
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
	var tags []ProbabilisticTag
	if err := json.Unmarshal([]byte(jsonStr), &tags); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	// Validate and clean up tags
	var validTags []ProbabilisticTag
	for _, tag := range tags {
		// Validate required fields
		if tag.Tag == "" || tag.Category == "" {
			continue
		}

		// Ensure confidence is in valid range
		if tag.Confidence < 0 {
			tag.Confidence = 0
		}
		if tag.Confidence > 1 {
			tag.Confidence = 1
		}

		// Clean up fields
		tag.Tag = strings.TrimSpace(tag.Tag)
		tag.Category = strings.TrimSpace(tag.Category)
		tag.Description = strings.TrimSpace(tag.Description)

		validTags = append(validTags, tag)
	}

	return validTags, nil
}

// GetPromptContext provides tag context for other AI tools
func (t *TagResolver) GetPromptContext(ctx context.Context, baggage map[string]interface{}) string {
	tags, ok := baggage["probabilistic_tags"].([]ProbabilisticTag)
	if !ok || len(tags) == 0 {
		return ""
	}

	var contextParts []string
	contextParts = append(contextParts, "### Detected Transaction Tags:")

	for i, tag := range tags {
		tagInfo := fmt.Sprintf("\nTag #%d:", i+1)
		tagInfo += fmt.Sprintf("\n- Name: %s (%s)", tag.Tag, tag.Category)
		tagInfo += fmt.Sprintf("\n- Confidence: %.1f%%", tag.Confidence*100)

		if len(tag.Evidence) > 0 {
			tagInfo += "\n- Evidence:"
			for _, evidence := range tag.Evidence {
				tagInfo += fmt.Sprintf("\n  • %s", evidence)
			}
		}

		if tag.Description != "" {
			tagInfo += fmt.Sprintf("\n- Description: %s", tag.Description)
		}

		contextParts = append(contextParts, tagInfo)
	}

	contextParts = append(contextParts, fmt.Sprintf("\n\nNote: Tags identified with %.1f%% minimum confidence threshold", t.confidenceThreshold*100))

	return strings.Join(contextParts, "")
}

// GetAnnotationContext provides annotation context for tags
func (t *TagResolver) GetAnnotationContext(ctx context.Context, baggage map[string]interface{}) *models.AnnotationContext {
	tags, ok := baggage["probabilistic_tags"].([]ProbabilisticTag)
	if !ok || len(tags) == 0 {
		return &models.AnnotationContext{Items: make([]models.AnnotationContextItem, 0)}
	}

	annotationContext := &models.AnnotationContext{
		Items: make([]models.AnnotationContextItem, 0),
	}

	for _, tag := range tags {
		description := fmt.Sprintf("%s tag (%.1f%% confidence)", tag.Category, tag.Confidence*100)
		if tag.Description != "" {
			description += fmt.Sprintf(": %s", tag.Description)
		}

		item := models.AnnotationContextItem{
			Type:        "tag",
			Value:       tag.Tag,
			Name:        tag.Tag,
			Description: description,
			Metadata: map[string]interface{}{
				"category":        tag.Category,
				"confidence":      tag.Confidence,
				"evidence":        tag.Evidence,
				"tag_description": tag.Description,
			},
		}

		annotationContext.AddItem(item)
	}

	return annotationContext
}
