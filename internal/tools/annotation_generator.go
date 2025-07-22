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

	prompt += `

INSTRUCTIONS:
1. Analyze the explanation text and identify EVERY element that should be annotated with links, tooltips, or icons
2. Use the context data above to provide accurate information - DO NOT skip elements that have context data available
3. For duplicate text (same text appearing multiple times), use index notation like "0@USDT" for first occurrence, "1@USDT" for second occurrence
4. MANDATORY ANNOTATIONS - you MUST annotate these if they appear in the text:
   - ANY token symbol (USDT, ETH, PEPE, GrowAI, etc.) - TABLE with: Token name, Symbol, Type, Decimals, Current price (if available), Contract address
   - ANY token amounts (100 USDT, 57,071 GrowAI, 0.5 ETH) - TABLE with: Amount, USD value (if available), Token name, Price per token, Contract address  
   - ANY gas fees ($0.82 gas, + $1.23 gas) - TABLE with: USD value, ETH value, Gas used, Gas price (Gwei), Total cost
   - ANY addresses (0x39e5...09c5, 0x1234...5678) - TABLE with: Address (shortened), ENS name (if available), Type (EOA/Contract), Link to explorer
   - ANY protocol names (1inch v6 aggregator, Uniswap, etc.) - TABLE with: Protocol name, Type (DEX/Aggregator/Lending), Function, Website link
   - ANY USD values ($100.00, $0.82) - TABLE with calculation breakdown and source data
5. EXPLORER LINKS - Always create links to blockchain explorers using the network context:
   - Token contract addresses → [NETWORK_EXPLORER]/token/[address]
   - Regular addresses → [NETWORK_EXPLORER]/address/[address]
   - Use the explorer URL from NETWORK CONTEXT above (e.g., https://etherscan.io for Ethereum)
6. PROTOCOL LINKS - Link protocol names to their websites (use context data when available):
   - "1inch" or "1inch v6 aggregator" → https://1inch.io
   - "Uniswap" → https://uniswap.org  
   - "Aave" → https://aave.com
7. BE COMPREHENSIVE - If there's context data for something, ANNOTATE IT. Don't skip elements.
8. PRICE INFORMATION - Always include USD prices when available in context data.

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

EXAMPLES:
[
  {
    "text": "USDT",
    "tooltip": "<table><tr><td><strong>Token:</strong></td><td>Tether USD</td></tr><tr><td><strong>Symbol:</strong></td><td>USDT</td></tr><tr><td><strong>Type:</strong></td><td>ERC20 Stablecoin</td></tr><tr><td><strong>Decimals:</strong></td><td>6</td></tr><tr><td><strong>Price:</strong></td><td>$1.00</td></tr><tr><td><strong>Contract:</strong></td><td>0xdAC17...ec7</td></tr></table>",
    "icon": "https://raw.githubusercontent.com/trustwallet/assets/refs/heads/master/blockchains/ethereum/assets/0xdAC17F958D2ee523a2206206994597C13D831ec7/logo.png"
  },
  {
    "text": "100 USDT",
    "link": "https://etherscan.io/token/0xdac17f958d2ee523a2206206994597c13d831ec7",
    "tooltip": "<table><tr><td><strong>Amount:</strong></td><td>100 USDT</td></tr><tr><td><strong>USD Value:</strong></td><td>$100.00</td></tr><tr><td><strong>Token:</strong></td><td>Tether USD</td></tr><tr><td><strong>Price:</strong></td><td>$1.00 per USDT</td></tr><tr><td><strong>Contract:</strong></td><td>0xdAC17...ec7</td></tr></table>",
    "icon": "https://raw.githubusercontent.com/trustwallet/assets/refs/heads/master/blockchains/ethereum/assets/0xdAC17F958D2ee523a2206206994597C13D831ec7/logo.png"
  },
  {
    "text": "$0.85 gas",
    "tooltip": "<table><tr><td><strong>Gas Fee:</strong></td><td>$0.85</td></tr><tr><td><strong>ETH Value:</strong></td><td>0.000234 ETH</td></tr><tr><td><strong>Gas Used:</strong></td><td>46,613</td></tr><tr><td><strong>Gas Price:</strong></td><td>12.5 Gwei</td></tr></table>"
  },
  {
    "text": "Uniswap V2 Router",
    "link": "https://uniswap.org",
    "tooltip": "<table><tr><td><strong>Protocol:</strong></td><td>Uniswap V2</td></tr><tr><td><strong>Type:</strong></td><td>DEX Router</td></tr><tr><td><strong>Function:</strong></td><td>Token Swapping</td></tr><tr><td><strong>Contract:</strong></td><td>0x7a25...88d</td></tr></table>",
    "icon": "https://raw.githubusercontent.com/trustwallet/assets/refs/heads/master/blockchains/ethereum/assets/0x1f9840a85d5aF5bf1D1762F925BDADdC4201F984/logo.png"
  },
  {
    "text": "USDT",
    "link": "https://etherscan.io/token/0xdac17f958d2ee523a2206206994597c13d831ec7",
    "tooltip": "<table><tr><td><strong>Token:</strong></td><td>Tether USD</td></tr><tr><td><strong>Symbol:</strong></td><td>USDT</td></tr><tr><td><strong>Price:</strong></td><td>$1.00</td></tr><tr><td><strong>Contract:</strong></td><td>0xdAC17...ec7</td></tr></table>",
    "icon": "https://raw.githubusercontent.com/trustwallet/assets/refs/heads/master/blockchains/ethereum/assets/0xdAC17F958D2ee523a2206206994597C13D831ec7/logo.png"
  },
  {
    "text": "GrowAI",
    "tooltip": "<table><tr><td><strong>Token:</strong></td><td>GrowAI</td></tr><tr><td><strong>Symbol:</strong></td><td>GrowAI</td></tr><tr><td><strong>Type:</strong></td><td>ERC20</td></tr><tr><td><strong>Contract:</strong></td><td>0x1234...5678</td></tr></table>",
    "icon": "https://raw.githubusercontent.com/trustwallet/assets/refs/heads/master/blockchains/ethereum/assets/0x1234567890abcdef1234567890abcdef12345678/logo.png"
  },
  {
    "text": "1inch v6 aggregator", 
    "link": "https://1inch.io",
    "tooltip": "<table><tr><td><strong>Protocol:</strong></td><td>1inch v6</td></tr><tr><td><strong>Type:</strong></td><td>DEX Aggregator</td></tr><tr><td><strong>Function:</strong></td><td>Optimal swap routing</td></tr><tr><td><strong>Website:</strong></td><td>1inch.io</td></tr></table>",
    "icon": "https://avatars.githubusercontent.com/u/62861014"
  },
  {
    "text": "0x39e5...09c5",
    "link": "https://etherscan.io/address/0x39e5c2e44c045e5ba25b55b2d6b3d7234399f09c5",
    "tooltip": "<table><tr><td><strong>Address:</strong></td><td>0x39e5...09c5</td></tr><tr><td><strong>Type:</strong></td><td>EOA (Wallet)</td></tr></table>"
  },
  {
    "text": "$0.82 gas",
    "tooltip": "<table><tr><td><strong>Gas Fee:</strong></td><td>$0.82</td></tr><tr><td><strong>ETH Value:</strong></td><td>0.000285 ETH</td></tr><tr><td><strong>Gas Used:</strong></td><td>193,811</td></tr></table>"
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