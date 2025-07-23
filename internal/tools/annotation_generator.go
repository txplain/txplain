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
	llm llms.Model

	verbose bool
}

// getKeys returns a slice of keys from a map for debugging
func getKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// NewAnnotationGenerator creates a new annotation generator
func NewAnnotationGenerator(llm llms.Model) *AnnotationGenerator {
	return &AnnotationGenerator{
		llm:     llm,
		verbose: false,
	}
}

// SetVerbose enables or disables verbose logging
func (ag *AnnotationGenerator) SetVerbose(verbose bool) {
	ag.verbose = verbose
}

// Dependencies returns the tools this processor depends on
func (ag *AnnotationGenerator) Dependencies() []string {
	return []string{"transaction_explainer"} // Must run after explanation is generated
}

// Process generates annotations for the explanation text
func (ag *AnnotationGenerator) Process(ctx context.Context, baggage map[string]interface{}) error {
	if ag.verbose {
		fmt.Println("\n" + strings.Repeat("📝", 60))
		fmt.Println("🔍 ANNOTATION GENERATOR: Starting interactive annotation generation")
		fmt.Printf("📦 Baggage keys available: %v\n", getKeys(baggage))
		fmt.Println(strings.Repeat("📝", 60))
	}
	
	// Get the explanation result
	explanation, ok := baggage["explanation"].(*models.ExplanationResult)
	if !ok || explanation == nil {
		if ag.verbose {
			fmt.Println("⚠️  No explanation found in baggage, skipping annotation generation")
			fmt.Println(strings.Repeat("📝", 60) + "\n")
		}
		return nil // Not an error, just nothing to annotate
	}

	if ag.verbose {
		fmt.Printf("📄 Explanation text length: %d characters\n", len(explanation.Summary))
	}

	// Collect context from all context providers using GetPromptContext
	var contextParts []string
	if contextProviders, ok := baggage["context_providers"].([]interface{}); ok {
		if ag.verbose {
			fmt.Printf("🔍 Found %d context providers\n", len(contextProviders))
		}
		for i, provider := range contextProviders {
			if toolProvider, ok := provider.(Tool); ok {
				context := toolProvider.GetPromptContext(ctx, baggage)
				if ag.verbose {
					fmt.Printf("   Provider[%d] (%T): Context length = %d\n", i, toolProvider, len(context))
				}
				if context != "" {
					contextParts = append(contextParts, context)
				}
			} else {
				if ag.verbose {
					fmt.Printf("   Provider[%d] (%T) does not implement Tool interface\n", i, provider)
				}
			}
		}
	} else {
		if ag.verbose {
			fmt.Println("⚠️  No context_providers found in baggage!")
			fmt.Printf("Available baggage keys: %v\n", getKeys(baggage))
		}
	}

	// Parse the text context to create annotation context
	contextText := strings.Join(contextParts, "\n\n")

	if ag.verbose {
		fmt.Printf("📊 Combined context from all providers: %d characters\n", len(contextText))
	}

	// Add network-specific context to the text
	networkContext := ""
	networkID := explanation.NetworkID
	if networkID > 0 {
		if network, exists := models.GetNetwork(networkID); exists {
			networkContext = fmt.Sprintf("\n### NETWORK CONTEXT:\n- Network: %s (ID: %d, Explorer: %s)",
				network.Name, networkID, network.Explorer)
		}
	}

	// Combine all context text
	fullContextText := contextText + networkContext

	if ag.verbose {
		fmt.Printf("📄 Full context text for LLM: %d characters\n", len(fullContextText))
	}

	// Generate annotations using AI with the text context
	annotations, err := ag.generateAnnotationsFromText(ctx, explanation.Summary, fullContextText)
	if err != nil {
		return fmt.Errorf("failed to generate annotations: %w", err)
	}

	// Add annotations to the explanation
	explanation.Annotations = annotations

	// Update baggage
	baggage["explanation"] = explanation

	if ag.verbose {
		fmt.Printf("📊 Generated %d annotations for text: '%s'\n", len(annotations), explanation.Summary)
		for i, annotation := range annotations {
			fmt.Printf("  Annotation[%d]: Text='%s', HasLink=%t, HasTooltip=%t, HasIcon=%t\n",
				i, annotation.Text, annotation.Link != "", annotation.Tooltip != "", annotation.Icon != "")
		}
		fmt.Println(strings.Repeat("📝", 60) + "\n")
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

// generateAnnotationsFromText uses AI to create annotations based on explanation text and context text
func (ag *AnnotationGenerator) generateAnnotationsFromText(ctx context.Context, explanationText string, contextText string) ([]models.Annotation, error) {
	// Build the prompt using the text context
	prompt := ag.buildAnnotationPromptFromText(explanationText, contextText)

	if ag.verbose {
		fmt.Println("\n" + strings.Repeat("=", 80))
		fmt.Println("🤖 ANNOTATION GENERATOR: LLM PROMPT")
		fmt.Println(strings.Repeat("=", 80))
		fmt.Println(prompt)
		fmt.Println(strings.Repeat("=", 80))
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

	// Get response text
	responseText := ""
	if response != nil && len(response.Choices) > 0 {
		responseText = response.Choices[0].Content
	}

	if ag.verbose {
		fmt.Println(strings.Repeat("=", 80))
		fmt.Println("🤖 ANNOTATION GENERATOR: LLM RESPONSE")
		fmt.Println(strings.Repeat("=", 80))
		fmt.Println(responseText)
		fmt.Println(strings.Repeat("=", 80) + "\n")
	}

	// Parse the response into annotations
	annotations, err := ag.parseAnnotationResponse(responseText)
	if err != nil {
		return nil, fmt.Errorf("failed to parse annotation response: %w", err)
	}

	return annotations, nil
}

// buildAnnotationPromptFromText creates the prompt for generating annotations from text context
func (ag *AnnotationGenerator) buildAnnotationPromptFromText(explanationText string, contextText string) string {
	if ag.verbose {
		fmt.Printf("AnnotationGenerator: Building prompt with context text\n")
		fmt.Printf("Context length: %d characters\n", len(contextText))
	}

	prompt := fmt.Sprintf(`You are an expert at creating interactive annotations for blockchain transaction explanations. Your task is to identify elements in the text that would benefit from links, tooltips, or icons to enhance user understanding.

EXPLANATION TEXT TO ANNOTATE:
"%s"

AVAILABLE CONTEXT DATA:
%s`, explanationText, contextText)

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
   - FOR ERC20/ERC721 TOKENS: Contract link format: [NETWORK_EXPLORER]/token/[contract_address] or [NETWORK_EXPLORER]/address/[contract_address]
   - FOR NATIVE TOKENS (ETH, BNB, MATIC): Link to main explorer homepage: [NETWORK_EXPLORER] (no contract address needed)
   - If contract address is in TOKEN CONTEXT above, use that exact address
   - If token is marked as "Native Token" or "native_token" type, DO NOT use a contract address - link to explorer homepage
   - If contract address is NOT in context, create a generic explorer link using blockchain explorer format
   - NEVER create a token symbol annotation without a "link" field - this is mandatory
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
    "text": "[NATIVE_TOKEN_SYMBOL like ETH]",
    "link": "[NETWORK_EXPLORER from NETWORK CONTEXT]",
    "tooltip": "<table><tr><td><strong>Token:</strong></td><td>[Native Token Name from CONTEXT]</td></tr><tr><td><strong>Symbol:</strong></td><td>[SYMBOL from CONTEXT]</td></tr><tr><td><strong>Type:</strong></td><td>Native Token</td></tr><tr><td><strong>Decimals:</strong></td><td>[decimals from CONTEXT]</td></tr><tr><td><strong>Price:</strong></td><td>[price from CONTEXT or 'Not available']</td></tr><tr><td><strong>Network:</strong></td><td>[network name - no contract address for native tokens]</td></tr></table>",
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

// GetRagContext provides RAG context for annotation information (minimal for this tool)
func (ag *AnnotationGenerator) GetRagContext(ctx context.Context, baggage map[string]interface{}) *RagContext {
	ragContext := NewRagContext()
	// Annotation generator processes UI/UX display data, not searchable knowledge for RAG
	return ragContext
}
