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
func NewTagResolver(llm llms.Model, verbose bool, confidenceThreshold float64) *TagResolver {
	resolver := &TagResolver{
		llm:                 llm,
		verbose:             verbose,
		confidenceThreshold: confidenceThreshold,
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
	if t.verbose {
		fmt.Println("\n" + strings.Repeat("🏷️", 60))
		fmt.Printf("🔍 TAG RESOLVER: Starting AI-powered tag detection (threshold: %.1f%%)\n", t.confidenceThreshold*100)
		fmt.Printf("📚 Knowledge base: %d tags loaded\n", len(t.tagKnowledge))
		fmt.Println(strings.Repeat("🏷️", 60))
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

	if t.verbose {
		fmt.Printf("📊 Built analysis context: %d characters\n", len(contextData))
	}

	// Use AI to identify tags with context from previous tools
	tags, err := t.identifyTagsWithAI(ctx, contextData)
	if err != nil {
		if t.verbose {
			fmt.Printf("❌ AI tag detection failed: %v\n", err)
			fmt.Println("⚠️  Falling back to empty tag list")
		}
		// Fall back to empty list - don't fail the whole pipeline
		tags = []ProbabilisticTag{}
	}

	if t.verbose && len(tags) > 0 {
		fmt.Printf("🧠 AI detected %d potential tags\n", len(tags))
	}

	// Filter by confidence threshold
	var highConfidenceTags []ProbabilisticTag
	for _, tag := range tags {
		if tag.Confidence >= t.confidenceThreshold {
			highConfidenceTags = append(highConfidenceTags, tag)
		}
	}

	if t.verbose {
		fmt.Printf("✅ Filtered to %d tags above %.1f%% confidence\n",
			len(highConfidenceTags), t.confidenceThreshold*100)

		// Show summary of detected tags
		if len(highConfidenceTags) > 0 {
			fmt.Println("\n📋 DETECTED TAGS SUMMARY:")
			for i, tag := range highConfidenceTags {
				fmt.Printf("   %d. %s (%s) - Confidence: %.1f%%\n",
					i+1, tag.Tag, tag.Category, tag.Confidence*100)
			}
		}

		fmt.Println("\n" + strings.Repeat("🏷️", 60))
		fmt.Println("✅ TAG RESOLVER: Completed successfully")
		fmt.Println(strings.Repeat("🏷️", 60) + "\n")
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

// identifyTagsWithAI uses LLM to identify tags from context
func (t *TagResolver) identifyTagsWithAI(ctx context.Context, contextData string) ([]ProbabilisticTag, error) {
	prompt := t.buildTagAnalysisPrompt(contextData)

	if t.verbose {
		fmt.Println("\n" + strings.Repeat("=", 80))
		fmt.Println("🤖 TAG RESOLVER: LLM PROMPT")
		fmt.Println(strings.Repeat("=", 80))
		fmt.Println(prompt)
		fmt.Println(strings.Repeat("=", 80))
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
		fmt.Println(strings.Repeat("=", 80))
		fmt.Println("🤖 TAG RESOLVER: LLM RESPONSE")
		fmt.Println(strings.Repeat("=", 80))
		fmt.Println(responseText)
		fmt.Println(strings.Repeat("=", 80) + "\n")
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

CRITICAL ANALYSIS RULES:
1. **ONLY identify tags when there is CLEAR EVIDENCE in the transaction context**  
2. **DO NOT assume token operations exist without explicit Transfer events or token-related method calls**
3. **DO NOT identify swap/liquidity tags unless there are actual Swap events or DEX-related calls**
4. **FOCUS on the actual events and method calls present in the transaction**
5. **Consider contract management operations, not just DeFi activities**

IDENTIFICATION PATTERNS:
- Method calls (transfer, approve, swap, mint, stake, grantRole, updateRole, etc.)
- Events emitted (Transfer, Swap, Approval, Mint, Burn, RoleGranted, UserRoleUpdated, etc.)
- Protocols involved (DEX, lending, NFT marketplace, access control, governance, etc.)
- Token types (ERC20, ERC721, ERC1155, stablecoins, etc.) - ONLY if actual tokens are involved
- Transaction patterns (multi-hop swaps, liquidity provision, flash loans, role management, etc.)
- Value flows (from/to addresses, amounts, fees)

CONFIDENCE CRITERIA:
- Explicit method/event matches = HIGH confidence (0.9-1.0)
- Strong protocol/pattern evidence = MEDIUM-HIGH confidence (0.7-0.9) 
- Probable indicators = MEDIUM confidence (0.5-0.7)
- Weak signals = LOW confidence (0.3-0.5)
- Speculation = VERY LOW confidence (0.0-0.3)

TAG SELECTION GUIDELINES:
- Be specific rather than generic (prefer "role-management" over "admin" when applicable)
- Include both protocol-specific and action-specific tags
- Consider the primary action and secondary effects
- Include technical standards when relevant (erc20, erc721, etc.) - BUT ONLY when actually present
- Add infrastructure tags for technical aspects (batch, proxy, multisig)
- Consider governance and access control operations

OUTPUT FORMAT:
Respond with a JSON array of tag identifications. Each should include:
{
  "tag": "tag-name",
  "category": "Category",
  "confidence": 0.85,
  "evidence": ["reason 1", "reason 2"],
  "description": "What this tag represents in this context"
}

DIVERSE EXAMPLES (covering different types of transactions):
[
  {
    "tag": "swap",
    "category": "DeFi",
    "confidence": 0.95,
    "evidence": ["Swap event emitted", "Uniswap protocol detected", "Token A->B exchange pattern"],
    "description": "Token swapping operation on a DEX"
  },
  {
    "tag": "role-management",
    "category": "Access Control",
    "confidence": 0.9,
    "evidence": ["UserRoleUpdated event emitted", "Role parameter indicates permission change", "Access control contract interaction"],
    "description": "Role or permission management in smart contract"
  },
  {
    "tag": "governance",
    "category": "DAO",
    "confidence": 0.85,
    "evidence": ["Vote cast event", "Governance contract interaction", "Proposal-related method call"],
    "description": "Governance participation or proposal management"
  },
  {
    "tag": "erc20",
    "category": "Token",
    "confidence": 0.9,
    "evidence": ["Transfer events", "ERC20 tokens involved", "approve/transfer methods"],
    "description": "ERC20 token standard operations"
  },
  {
    "tag": "contract-deployment",
    "category": "Infrastructure",
    "confidence": 0.95,
    "evidence": ["Contract creation transaction", "Zero address as recipient", "Constructor execution"],
    "description": "Smart contract deployment operation"
  }
]

**CRITICAL VALIDATION RULES:**
- If there are NO Transfer events, do NOT tag as "token-transfer" or "erc20"
- If there are NO Swap events, do NOT tag as "swap" or "liquidity"  
- If there are NO mint/burn operations, do NOT tag as "minting"
- ONLY use DeFi tags when actual DeFi protocols/operations are detected
- ALWAYS require clear evidence from the transaction context

Analyze the transaction context and return tags with reasonable confidence (> 0.3). Focus on accuracy - it's better to miss a tag than to incorrectly identify one. Limit to 8-10 most relevant tags.`,
		knowledgeContext.String(), contextData)

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

// GetRagContext provides RAG context for tag information
func (t *TagResolver) GetRagContext(ctx context.Context, baggage map[string]interface{}) *RagContext {
	ragContext := NewRagContext()

	tags, ok := baggage["tags_detailed"].([]ProbabilisticTag)
	if !ok || len(tags) == 0 {
		return ragContext
	}

	// Add tag information to RAG context for searchability
	for _, tag := range tags {
		if tag.Confidence >= t.confidenceThreshold {
			ragContext.AddItem(RagContextItem{
				ID:      fmt.Sprintf("tag_%s", strings.ReplaceAll(tag.Tag, "-", "_")),
				Type:    "tag",
				Title:   fmt.Sprintf("%s Tag", tag.Tag),
				Content: fmt.Sprintf("Tag %s in category %s represents %s with confidence %.2f", tag.Tag, tag.Category, tag.Description, tag.Confidence),
				Metadata: map[string]interface{}{
					"tag":         tag.Tag,
					"category":    tag.Category,
					"description": tag.Description,
					"confidence":  tag.Confidence,
					"evidence":    tag.Evidence,
				},
				Keywords:  []string{tag.Tag, tag.Category, "tag"},
				Relevance: float64(tag.Confidence),
			})
		}
	}

	return ragContext
}
