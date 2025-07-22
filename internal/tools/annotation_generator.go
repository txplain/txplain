package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/tmc/langchaingo/llms"
	"github.com/txplain/txplain/internal/models"
)

// AnnotationGenerator creates interactive annotations for explanation text using AI
type AnnotationGenerator struct {
	llm               llms.Model
	contextCollector  *AnnotationContextCollector
	verbose           bool
}

// NewAnnotationGenerator creates a new annotation generator
func NewAnnotationGenerator(llm llms.Model) *AnnotationGenerator {
	return &AnnotationGenerator{
		llm:              llm,
		contextCollector: NewAnnotationContextCollector(),
		verbose:          false,
	}
}

// SetVerbose enables or disables verbose logging
func (ag *AnnotationGenerator) SetVerbose(verbose bool) {
	ag.verbose = verbose
}

// AddContextProvider adds a context provider to the collector
func (ag *AnnotationGenerator) AddContextProvider(provider AnnotationContextProvider) {
	ag.contextCollector.AddProvider(provider)
}

// Dependencies returns the tools this processor depends on
func (ag *AnnotationGenerator) Dependencies() []string {
	return []string{"transaction_explainer"} // Must run after explanation is generated
}

// Process generates annotations for the explanation text
func (ag *AnnotationGenerator) Process(ctx context.Context, baggage map[string]interface{}) error {
	// Get the explanation result
	explanation, ok := baggage["explanation"].(*models.ExplanationResult)
	if !ok || explanation == nil {
		if ag.verbose {
			fmt.Println("AnnotationGenerator: No explanation found in baggage, skipping")
		}
		return nil // Not an error, just nothing to annotate
	}

	// Collect annotation context from all providers
	annotationContext := ag.contextCollector.Collect(ctx, baggage)
	
	if ag.verbose {
		fmt.Printf("AnnotationGenerator: Collected %d context items from %d providers\n", 
			len(annotationContext.Items), len(ag.contextCollector.providers))
		
		// Count by type
		typeCounts := make(map[string]int)
		for _, item := range annotationContext.Items {
			typeCounts[item.Type]++
		}
		for itemType, count := range typeCounts {
			fmt.Printf("  %s items: %d\n", itemType, count)
		}
	}

	// Add network-specific context for explorer links
	networkID := explanation.NetworkID
	if networkID > 0 {
		if network, exists := models.GetNetwork(networkID); exists {
			// Add network context for explorer links
			annotationContext.AddItem(models.AnnotationContextItem{
				Type:        "network",
				Value:       fmt.Sprintf("%d", networkID),
				Name:        network.Name,
				Link:        network.Explorer,
				Description: fmt.Sprintf("%s blockchain explorer", network.Name),
				Metadata: map[string]interface{}{
					"network_id": networkID,
					"explorer":   network.Explorer,
					"name":       network.Name,
				},
			})
		}
	}

	// Generate annotations using AI
	annotations, err := ag.generateAnnotations(ctx, explanation.Summary, annotationContext)
	if err != nil {
		return fmt.Errorf("failed to generate annotations: %w", err)
	}

	// Add annotations to the explanation
	explanation.Annotations = annotations

	// Update baggage
	baggage["explanation"] = explanation

	if ag.verbose {
		fmt.Printf("AnnotationGenerator: Generated %d annotations for text: '%s'\n", len(annotations), explanation.Summary)
		for i, annotation := range annotations {
			fmt.Printf("  Annotation[%d]: Text='%s', HasLink=%t, HasTooltip=%t, HasIcon=%t\n", 
				i, annotation.Text, annotation.Link != "", annotation.Tooltip != "", annotation.Icon != "")
		}
	}

	return nil
}

// Name returns the tool name
func (ag *AnnotationGenerator) Name() string {
	return "annotation_generator"
}

// Description returns the tool description
func (ag *AnnotationGenerator) Description() string {
	return "Generates interactive annotations (links, tooltips, icons) for explanation text using AI"
}

// generateAnnotations uses AI to create annotations based on explanation text and available context
func (ag *AnnotationGenerator) generateAnnotations(ctx context.Context, explanationText string, annotationContext *models.AnnotationContext) ([]models.Annotation, error) {
	// Build the prompt
	prompt := ag.buildAnnotationPrompt(explanationText, annotationContext)

	if ag.verbose {
		fmt.Println("=== ANNOTATION GENERATOR: PROMPT SENT TO LLM ===")
		fmt.Println(prompt)
		fmt.Println("=== END OF PROMPT ===")
		fmt.Println()
	}

	// Call LLM
	response, err := ag.llm.GenerateContent(ctx, []llms.MessageContent{
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

	// Parse the LLM response
	responseText := ""
	if response != nil && len(response.Choices) > 0 {
		responseText = response.Choices[0].Content
	}

	if ag.verbose {
		fmt.Println("=== ANNOTATION GENERATOR: LLM RESPONSE ===")
		fmt.Println(responseText)
		fmt.Println("=== END OF LLM RESPONSE ===")
		fmt.Println()
	}

	// Parse the response into annotations
	annotations, err := ag.parseAnnotationResponse(responseText)
	if err != nil {
		return nil, fmt.Errorf("failed to parse annotation response: %w", err)
	}

	return annotations, nil
}

// buildAnnotationPrompt creates the prompt for generating annotations
func (ag *AnnotationGenerator) buildAnnotationPrompt(explanationText string, annotationContext *models.AnnotationContext) string {
	if ag.verbose {
		fmt.Printf("AnnotationGenerator: Building prompt with %d context items\n", len(annotationContext.Items))
		for i, item := range annotationContext.Items {
			fmt.Printf("  Context[%d]: Type=%s, Value=%s, Name=%s, Icon=%s, Link=%s\n", 
				i, item.Type, item.Value, item.Name, item.Icon, item.Link)
		}
	}

	prompt := fmt.Sprintf(`You are an expert at creating interactive annotations for blockchain transaction explanations. Your task is to identify elements in the text that would benefit from links, tooltips, or icons to enhance user understanding.

EXPLANATION TEXT TO ANNOTATE:
"%s"

AVAILABLE CONTEXT DATA:
`, explanationText)

	// Add context data organized by type
	tokenItems := []models.AnnotationContextItem{}
	addressItems := []models.AnnotationContextItem{}
	protocolItems := []models.AnnotationContextItem{}
	amountItems := []models.AnnotationContextItem{}
	networkItems := []models.AnnotationContextItem{}
	addressMappingItems := []models.AnnotationContextItem{}
	gasFeeItems := []models.AnnotationContextItem{}

	for _, item := range annotationContext.Items {
		switch item.Type {
		case "token":
			tokenItems = append(tokenItems, item)
		case "address":
			addressItems = append(addressItems, item)
		case "protocol":
			protocolItems = append(protocolItems, item)
		case "amount":
			amountItems = append(amountItems, item)
		case "network":
			networkItems = append(networkItems, item)
		case "address_mapping", "ens":
			addressMappingItems = append(addressMappingItems, item)
		case "gas_fee":
			gasFeeItems = append(gasFeeItems, item)
		}
	}

	if len(tokenItems) > 0 {
		prompt += "\nTOKEN CONTEXT:\n"
		for _, item := range tokenItems {
			prompt += fmt.Sprintf("- %s: %s", item.Value, item.Name)
			if item.Icon != "" {
				prompt += fmt.Sprintf(" (Icon: %s)", item.Icon)
			}
			if item.Description != "" {
				prompt += fmt.Sprintf(" - %s", item.Description)
			}
			prompt += "\n"
		}
	}

	if len(addressItems) > 0 {
		prompt += "\nADDRESS CONTEXT:\n"
		for _, item := range addressItems {
			prompt += fmt.Sprintf("- %s: %s", item.Value, item.Name)
			if item.Link != "" {
				prompt += fmt.Sprintf(" (Link: %s)", item.Link)
			}
			if item.Description != "" {
				prompt += fmt.Sprintf(" - %s", item.Description)
			}
			prompt += "\n"
		}
	}

	if len(protocolItems) > 0 {
		prompt += "\nPROTOCOL CONTEXT:\n"
		for _, item := range protocolItems {
			prompt += fmt.Sprintf("- %s: %s", item.Value, item.Name)
			if item.Icon != "" {
				prompt += fmt.Sprintf(" (Icon: %s)", item.Icon)
			}
			if item.Link != "" {
				prompt += fmt.Sprintf(" (Link: %s)", item.Link)
			}
			if item.Description != "" {
				prompt += fmt.Sprintf(" - %s", item.Description)
			}
			prompt += "\n"
		}
	}

	if len(networkItems) > 0 {
		prompt += "\nNETWORK CONTEXT:\n"
		for _, item := range networkItems {
			prompt += fmt.Sprintf("- %s: %s", item.Value, item.Name)
			if item.Link != "" {
				prompt += fmt.Sprintf(" (Explorer: %s)", item.Link)
			}
			if item.Description != "" {
				prompt += fmt.Sprintf(" - %s", item.Description)
			}
			prompt += "\n"
		}
	}

	if len(addressMappingItems) > 0 {
		prompt += "\nADDRESS MAPPING CONTEXT:\n"
		for _, item := range addressMappingItems {
			prompt += fmt.Sprintf("- %s: %s", item.Value, item.Name)
			if item.Link != "" {
				prompt += fmt.Sprintf(" (Link: %s)", item.Link)
			}
			if item.Description != "" {
				prompt += fmt.Sprintf(" - %s", item.Description)
			}
			prompt += "\n"
		}
	}

	if len(gasFeeItems) > 0 {
		prompt += "\nGAS FEE CONTEXT:\n"
		for _, item := range gasFeeItems {
			prompt += fmt.Sprintf("- %s: %s", item.Value, item.Name)
			if tooltip, ok := item.Metadata["tooltip"].(string); ok {
				prompt += fmt.Sprintf(" (Tooltip: %s)", tooltip)
			}
			if item.Description != "" {
				prompt += fmt.Sprintf(" - %s", item.Description)
			}
			prompt += "\n"
		}
	}

	prompt += `

INSTRUCTIONS:
1. Analyze the explanation text and identify EVERY element that should be annotated with links, tooltips, or icons
2. Use the context data above to provide accurate information - DO NOT skip elements that have context data available
3. For duplicate text (same text appearing multiple times), use index notation like "0@USDT" for first occurrence, "1@USDT" for second occurrence
4. MANDATORY ANNOTATIONS - you MUST annotate these if they appear in the text:
   - ANY token symbol (USDT, ETH, PEPE, GrowAI, etc.) - TABLE with: Token name, Symbol, Type, Decimals, Current price (if available), Contract address
   - ANY token amounts (100 USDT, 57,071 GrowAI, 0.5 ETH) - TABLE with: Amount, USD value (if available), Token name, Price per token, Contract address  
   - ANY gas fees ($0.82 gas, + $1.23 gas, $1.024) - Use GAS FEE CONTEXT above to provide detailed breakdown with: USD value, Native token value, Gas used, Gas price (Gwei), Total cost
   - ANY addresses (0x39e5...09c5, 0x1234...5678) - TABLE with: Address (shortened), ENS name (if available), Type (EOA/Contract), Link to explorer
   - ANY protocol names (1inch v6 aggregator, Uniswap, etc.) - TABLE with: Protocol name, Type (DEX/Aggregator/Lending), Function, Website link
   - ANY USD values ($100.00, $0.82) - TABLE with calculation breakdown and source data
5. CRITICAL TOKEN SYMBOL LINKING RULE:
   - EVERY token symbol (USDT, PEPE, GrowAI, etc.) MUST ALWAYS include a "link" field pointing to the contract address
   - Contract link format: [NETWORK_EXPLORER]/token/[contract_address] or [NETWORK_EXPLORER]/address/[contract_address]
   - If contract address is in TOKEN CONTEXT above, use that exact address
   - If contract address is NOT in context, create a generic explorer link using blockchain explorer format
   - NEVER create a token symbol annotation without a contract link - this is mandatory
6. EXPLORER LINKS - Always create links to blockchain explorers using the network context:
   - Token contract addresses → [NETWORK_EXPLORER]/token/[address]
   - Regular addresses → [NETWORK_EXPLORER]/address/[address]
   - Use the explorer URL from NETWORK CONTEXT above (e.g., https://etherscan.io for Ethereum)
   - CRITICAL ADDRESS MAPPING: Use ADDRESS MAPPING CONTEXT to convert shortened addresses to full addresses
   - If you see a shortened address like "0x22d4...ba3" in the text, look up the full address in ADDRESS MAPPING CONTEXT
   - Example: "0x22d4...ba3" maps to "0x000000000022d473030f116ddee9f6b43ac78ba3" for explorer links
   - NEVER use shortened addresses directly in explorer URLs - always use the full address from the mapping
7. PROTOCOL LINKS - Link protocol names to their websites (ONLY use context data when available):
   - Extract protocol website URLs from PROTOCOL CONTEXT above - DO NOT hardcode any URLs
   - If no protocol context data available, do not create protocol links
   - Generic approach: work only with provided context data
8. BE COMPREHENSIVE - If there's context data for something, ANNOTATE IT. Don't skip elements.
9. CRITICAL USD PRICE REQUIREMENTS:
   - If TOKEN CONTEXT contains current USD price data, you MUST include it in tooltips
   - Price format: "$X.XX per [SYMBOL]" or "Current price: $X.XX"
   - For token amounts, calculate and show total USD value: "[AMOUNT] × $[PRICE] = $[TOTAL]"
   - If no price data available, state "Price: Not available" in tooltip
   - NEVER omit available pricing information from tooltips
10. TOKEN ANNOTATION REQUIREMENTS:
    - ONLY use data that is explicitly provided in the TOKEN CONTEXT above
    - If a token symbol appears but has no context data, create a basic annotation stating "Token information not available"
    - DO NOT make assumptions about contract addresses, prices, or decimals for tokens not in context
    - Generic approach: work with whatever data is provided, don't hardcode specific tokens
11. CRITICAL TOKEN SYMBOL ANNOTATION REQUIREMENTS - NO EXCEPTIONS:
    - EVERY token symbol annotation MUST include BOTH "link" AND "tooltip" fields - NO EXCEPTIONS
    - Link must point to contract address on blockchain explorer: [NETWORK_EXPLORER]/token/[contract_address]
    - If you create a token symbol annotation without a "link" field, you have FAILED the task
    - Tooltip must include token name, symbol, contract address, and current USD price if available
    - For unknown tokens, use fallback addresses or create explorer address links

ANNOTATION RULES:
- text: The exact text to match from the explanation (use index for duplicates: "0@100 USDT")
- link: URL to link to (optional) - use explorer links for addresses, protocol websites for protocols
- tooltip: HTML tooltip content (optional) - USE TABLES for structured data, include comprehensive details
- icon: Icon URL or path (optional) - use token icons, protocol logos, etc.

TOOLTIP FORMATTING GUIDELINES:
- Use HTML tables for structured information: <table><tr><td>Label:</td><td>Value</td></tr></table>
- Include comprehensive data for each element type
- Keep tables compact but informative - use concise values when possible
- Use <strong> for headers and important values
- Contract addresses should be shortened (0x1234...5678 format) unless full address is specifically needed
- Keep table rows focused on most valuable information - prioritize what users most need to know

OUTPUT FORMAT:
Respond with a JSON array of annotation objects. Each object should have:
{
  "text": "exact text to match",
  "link": "optional URL",
  "tooltip": "optional HTML tooltip",
  "icon": "optional icon URL"
}

CRITICAL FINAL VALIDATION:
Before outputting, verify that EVERY token symbol annotation (USDT, GrowAI, PEPE, etc.) includes:
✓ "text" field with the token symbol
✓ "link" field pointing to blockchain explorer 
✓ "tooltip" field with comprehensive token data
✓ "icon" field if available
Any token symbol annotation missing a "link" field is INVALID and must be corrected.

EXAMPLES FORMAT (use ONLY context data, never hardcode):
[
  {
    "text": "[TOKEN_SYMBOL from text]",
    "link": "[NETWORK_EXPLORER]/token/[contract_address from TOKEN CONTEXT]",
    "tooltip": "<table><tr><td><strong>Token:</strong></td><td>[Token Name from TOKEN CONTEXT]</td></tr><tr><td><strong>Symbol:</strong></td><td>[SYMBOL from TOKEN CONTEXT]</td></tr><tr><td><strong>Type:</strong></td><td>[Type from TOKEN CONTEXT]</td></tr><tr><td><strong>Decimals:</strong></td><td>[decimals from TOKEN CONTEXT]</td></tr><tr><td><strong>Price:</strong></td><td>[price from TOKEN CONTEXT or 'Not available']</td></tr><tr><td><strong>Contract:</strong></td><td>[shortened contract from TOKEN CONTEXT]</td></tr></table>",
    "icon": "[icon from TOKEN CONTEXT if available]"
  },
  {
    "text": "[TOKEN_AMOUNT from text]",
    "link": "[NETWORK_EXPLORER]/token/[contract_address from TOKEN CONTEXT]",
    "tooltip": "<table><tr><td><strong>Amount:</strong></td><td>[AMOUNT from text]</td></tr><tr><td><strong>USD Value:</strong></td><td>[calculated from TOKEN CONTEXT price]</td></tr><tr><td><strong>Token:</strong></td><td>[Token Name from TOKEN CONTEXT]</td></tr><tr><td><strong>Price:</strong></td><td>[price per token from TOKEN CONTEXT]</td></tr><tr><td><strong>Contract:</strong></td><td>[shortened contract from TOKEN CONTEXT]</td></tr></table>",
    "icon": "[icon from TOKEN CONTEXT if available]"
  },
  {
    "text": "[PROTOCOL_NAME from text]",
    "link": "[website URL from PROTOCOL CONTEXT]",
    "tooltip": "<table><tr><td><strong>Protocol:</strong></td><td>[Protocol name from PROTOCOL CONTEXT]</td></tr><tr><td><strong>Type:</strong></td><td>[Type from PROTOCOL CONTEXT]</td></tr><tr><td><strong>Function:</strong></td><td>[Description from PROTOCOL CONTEXT]</td></tr><tr><td><strong>Website:</strong></td><td>[Link from PROTOCOL CONTEXT]</td></tr></table>",
    "icon": "[icon from PROTOCOL CONTEXT if available]"
  },
  {
    "text": "[ADDRESS from text]",
    "link": "[NETWORK_EXPLORER]/address/[address]",
    "tooltip": "<table><tr><td><strong>Address:</strong></td><td>[shortened address]</td></tr><tr><td><strong>Type:</strong></td><td>[Type from ADDRESS CONTEXT]</td></tr><tr><td><strong>Name:</strong></td><td>[Name from ADDRESS CONTEXT if available]</td></tr></table>"
  },
  {
    "text": "[SHORTENED_ADDRESS from text like 0x22d4...ba3]",
    "link": "[NETWORK_EXPLORER]/address/[FULL_ADDRESS from ADDRESS MAPPING CONTEXT]",
    "tooltip": "<table><tr><td><strong>Address:</strong></td><td>[shortened address display]</td></tr><tr><td><strong>Full Address:</strong></td><td>[full address from mapping]</td></tr><tr><td><strong>ENS:</strong></td><td>[ENS name if available]</td></tr></table>"
  },
  {
    "text": "[GAS_FEE from text like $1.024 gas or $0.82]",
    "tooltip": "[Use the exact tooltip from GAS FEE CONTEXT above - DO NOT modify it]"
  }
]

Generate annotations for the explanation text above:`

	return prompt
}

// parseAnnotationResponse parses the AI response into annotation objects
func (ag *AnnotationGenerator) parseAnnotationResponse(response string) ([]models.Annotation, error) {
	// Clean up the response - extract JSON from potential markdown or other formatting
	response = strings.TrimSpace(response)
	
	// Look for JSON array in the response
	jsonStart := strings.Index(response, "[")
	jsonEnd := strings.LastIndex(response, "]")
	
	if jsonStart == -1 || jsonEnd == -1 || jsonEnd <= jsonStart {
		return nil, fmt.Errorf("no valid JSON array found in response")
	}
	
	jsonStr := response[jsonStart : jsonEnd+1]
	
	// Parse JSON
	var annotations []models.Annotation
	if err := json.Unmarshal([]byte(jsonStr), &annotations); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}
	
	// Validate and clean up annotations
	var validAnnotations []models.Annotation
	for _, annotation := range annotations {
		if annotation.Text == "" {
			continue // Skip annotations without text
		}
		
		// Clean up text field
		annotation.Text = strings.TrimSpace(annotation.Text)
		
		// Validate URLs
		if annotation.Link != "" && !ag.isValidURL(annotation.Link) {
			annotation.Link = "" // Clear invalid URLs
		}
		
		if annotation.Icon != "" && !ag.isValidURL(annotation.Icon) {
			annotation.Icon = "" // Clear invalid icon URLs
		}
		
		validAnnotations = append(validAnnotations, annotation)
	}
	
	return validAnnotations, nil
}

// isValidURL performs basic URL validation
func (ag *AnnotationGenerator) isValidURL(url string) bool {
	// Basic URL pattern matching
	urlPattern := regexp.MustCompile(`^https?://[^\s]+$`)
	return urlPattern.MatchString(url)
}

// GetPromptContext provides context for other tools (not used by this tool)
func (ag *AnnotationGenerator) GetPromptContext(ctx context.Context, baggage map[string]interface{}) string {
	return "" // This tool doesn't provide context to others
} 