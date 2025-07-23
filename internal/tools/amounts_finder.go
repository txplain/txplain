package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tmc/langchaingo/llms"
	"github.com/txplain/txplain/internal/models"
)

// AmountsFinder uses LLM intelligence to identify all relevant monetary amounts in transactions
type AmountsFinder struct {
	llm llms.Model
}

// DetectedAmount represents a monetary amount found by the LLM
type DetectedAmount struct {
	Amount        string  `json:"amount"`         // Raw amount value (hex or decimal)
	TokenContract string  `json:"token_contract"` // Contract address of the token
	TokenSymbol   string  `json:"token_symbol"`   // Token symbol if known
	AmountType    string  `json:"amount_type"`    // "transfer", "fee", "swap_in", "swap_out", "mint", "burn", etc.
	Context       string  `json:"context"`        // Where this amount was found (event name, parameter name, etc.)
	Confidence    float64 `json:"confidence"`     // Confidence level 0-1
	FromAddress   string  `json:"from_address"`   // Address sending the amount (if applicable)
	ToAddress     string  `json:"to_address"`     // Address receiving the amount (if applicable)
	EventContract string  `json:"event_contract"` // Contract that emitted the event containing this amount
	IsValidToken  bool    `json:"is_valid_token"` // Whether the token contract is validated as ERC20
}

// NewAmountsFinder creates a new amounts finder
func NewAmountsFinder(llm llms.Model) *AmountsFinder {
	return &AmountsFinder{
		llm: llm,
	}
}

// Name returns the tool name
func (a *AmountsFinder) Name() string {
	return "amounts_finder"
}

// Description returns the tool description
func (a *AmountsFinder) Description() string {
	return "Uses LLM intelligence to analyze transaction context and identify ALL relevant monetary amounts and their associated token contracts"
}

// Dependencies returns the tools this processor depends on
func (a *AmountsFinder) Dependencies() []string {
	return []string{"abi_resolver", "log_decoder", "trace_decoder", "token_metadata_enricher"}
}

// Process analyzes transaction context and identifies all relevant amounts
func (a *AmountsFinder) Process(ctx context.Context, baggage map[string]interface{}) error {
	fmt.Println("=== AMOUNTS FINDER: STARTING PROCESSING ===")

	// Build comprehensive context for the LLM
	contextData := a.buildAnalysisContext(baggage)

	fmt.Printf("AMOUNTS FINDER: Built context with %d characters\n", len(contextData))

	// Use LLM to identify all amounts
	detectedAmounts, err := a.identifyAmountsWithLLM(ctx, contextData, baggage)
	if err != nil {
		fmt.Printf("AMOUNTS FINDER: LLM call failed: %v\n", err)
		return fmt.Errorf("failed to identify amounts with LLM: %w", err)
	}

	fmt.Printf("AMOUNTS FINDER: LLM detected %d amounts\n", len(detectedAmounts))

	// Validate token contracts and filter results
	validatedAmounts := a.validateTokenContracts(detectedAmounts, baggage)

	fmt.Printf("AMOUNTS FINDER: Validated %d amounts\n", len(validatedAmounts))

	// Add to baggage for downstream tools
	baggage["detected_amounts"] = validatedAmounts

	fmt.Println("=== AMOUNTS FINDER: COMPLETED PROCESSING ===")
	return nil
}

// buildAnalysisContext creates comprehensive context for LLM analysis
func (a *AmountsFinder) buildAnalysisContext(baggage map[string]interface{}) string {
	var contextParts []string

	// Add ABI resolver context (contract information)
	if resolvedContracts, ok := baggage["resolved_contracts"].(map[string]*ContractInfo); ok && len(resolvedContracts) > 0 {
		contextParts = append(contextParts, "### VERIFIED CONTRACTS:")
		for address, contract := range resolvedContracts {
			if contract.IsVerified {
				contractInfo := fmt.Sprintf("- %s", address)
				if contract.ContractName != "" {
					contractInfo += fmt.Sprintf(" (%s)", contract.ContractName)
				}
				contextParts = append(contextParts, contractInfo)

				// Add ABI methods and events for context
				if len(contract.ParsedABI) > 0 {
					var events, functions []string
					for _, method := range contract.ParsedABI {
						if method.Type == "event" {
							events = append(events, method.Name)
						} else if method.Type == "function" {
							functions = append(functions, method.Name)
						}
					}
					if len(events) > 0 {
						contextParts = append(contextParts, fmt.Sprintf("  • Events: %s", strings.Join(events, ", ")))
					}
					if len(functions) > 0 {
						contextParts = append(contextParts, fmt.Sprintf("  • Functions: %s", strings.Join(functions, ", ")))
					}
				}
			}
		}
	}

	// Add token metadata context
	if tokenMetadata, ok := baggage["token_metadata"].(map[string]*TokenMetadata); ok && len(tokenMetadata) > 0 {
		contextParts = append(contextParts, "\n### KNOWN TOKEN CONTRACTS:")
		for address, metadata := range tokenMetadata {
			tokenInfo := fmt.Sprintf("- %s: %s (%s) - %d decimals",
				address, metadata.Name, metadata.Symbol, metadata.Decimals)
			contextParts = append(contextParts, tokenInfo)
		}
	}

	// Add decoded events with full parameter information
	if events, ok := baggage["events"].([]models.Event); ok && len(events) > 0 {
		contextParts = append(contextParts, "\n### DECODED EVENTS:")
		for i, event := range events {
			eventInfo := fmt.Sprintf("Event #%d: %s on contract %s", i+1, event.Name, event.Contract)
			contextParts = append(contextParts, eventInfo)

			if event.Parameters != nil {
				contextParts = append(contextParts, "  Parameters:")
				for paramName, paramValue := range event.Parameters {
					contextParts = append(contextParts, fmt.Sprintf("    - %s: %v", paramName, paramValue))
				}
			}
		}
	}

	// Add trace data (function calls and their parameters)
	if rawData, ok := baggage["raw_data"].(map[string]interface{}); ok {
		if trace, ok := rawData["trace"].(map[string]interface{}); ok {
			contextParts = append(contextParts, "\n### FUNCTION CALLS:")
			if traceResult, ok := trace["result"].(map[string]interface{}); ok {
				contextParts = append(contextParts, fmt.Sprintf("- Main call: %v", traceResult))

				if calls, ok := traceResult["calls"].([]interface{}); ok {
					for i, call := range calls {
						if callMap, ok := call.(map[string]interface{}); ok {
							contextParts = append(contextParts, fmt.Sprintf("- Sub-call #%d: %v", i+1, callMap))
						}
					}
				}
			}
		}
	}

	// Add raw transaction data for completeness
	if rawData, ok := baggage["raw_data"].(map[string]interface{}); ok {
		if tx, ok := rawData["transaction"].(map[string]interface{}); ok {
			contextParts = append(contextParts, "\n### TRANSACTION DATA:")
			if value, ok := tx["value"].(string); ok && value != "0x0" {
				contextParts = append(contextParts, fmt.Sprintf("- Native token value: %s", value))
			}
			if input, ok := tx["input"].(string); ok && input != "0x" {
				contextParts = append(contextParts, fmt.Sprintf("- Input data: %s", input))
			}
		}
	}

	return strings.Join(contextParts, "\n")
}

// identifyAmountsWithLLM uses LLM to identify all relevant amounts
func (a *AmountsFinder) identifyAmountsWithLLM(ctx context.Context, contextData string, baggage map[string]interface{}) ([]DetectedAmount, error) {
	prompt := a.buildAmountAnalysisPrompt(contextData)

	// Call LLM
	response, err := a.llm.GenerateContent(ctx, []llms.MessageContent{
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

	// Parse response
	var amounts []DetectedAmount
	responseText := response.Choices[0].Content

	// Extract JSON from response (handle potential markdown formatting)
	jsonStart := strings.Index(responseText, "[")
	jsonEnd := strings.LastIndex(responseText, "]")

	if jsonStart == -1 || jsonEnd == -1 || jsonEnd <= jsonStart {
		return nil, fmt.Errorf("invalid JSON response format")
	}

	jsonStr := responseText[jsonStart : jsonEnd+1]

	if err := json.Unmarshal([]byte(jsonStr), &amounts); err != nil {
		return nil, fmt.Errorf("failed to parse LLM response: %w", err)
	}

	return amounts, nil
}

// buildAmountAnalysisPrompt creates the prompt for LLM amount analysis
func (a *AmountsFinder) buildAmountAnalysisPrompt(contextData string) string {
	return fmt.Sprintf(`You are an expert blockchain transaction analyzer. Your task is to identify ALL relevant monetary amounts in this transaction and their associated token contracts.

TRANSACTION CONTEXT:
%s

ANALYSIS INSTRUCTIONS:

1. **IDENTIFY TOKEN AMOUNTS**: Look for any numeric values that represent token amounts, including:
   - Event parameters like "value", "amount", "quantity", "balance", etc.
   - Function call parameters that contain amounts
   - Native token values in transaction data
   - Any hex or decimal numbers that could represent token amounts

2. **ASSOCIATE WITH TOKENS**: For each amount, determine:
   - The token contract address that this amount refers to
   - The token symbol if available from the context
   - Whether this is a known ERC20 token based on available metadata

3. **CATEGORIZE AMOUNTS**: Classify each amount by type:
   - "transfer" - standard token transfers between addresses
   - "swap_in" - tokens going into a swap/exchange
   - "swap_out" - tokens coming out of a swap/exchange  
   - "fee" - fees paid (protocol fees, gas, etc.)
   - "mint" - newly created tokens (from zero address)
   - "burn" - destroyed tokens (to zero address)
   - "deposit" - tokens deposited into a protocol
   - "withdraw" - tokens withdrawn from a protocol
   - "other" - any other type of amount

4. **VALIDATION RULES**:
   - Only include amounts that have clear token contract associations
   - Skip amounts of zero or empty values
   - Skip raw transaction data that doesn't represent token amounts
   - Focus on meaningful amounts that represent actual value transfers

5. **CONFIDENCE SCORING**:
   - 0.9-1.0: Clear token contract + verified metadata + explicit amount parameter
   - 0.7-0.9: Clear token contract + amount parameter, but less context
   - 0.5-0.7: Inferred token contract + probable amount
   - 0.3-0.5: Uncertain token association or amount interpretation
   - Below 0.3: Don't include

OUTPUT FORMAT:
Respond with a JSON array of detected amounts. Each should include:

{
  "amount": "hex or decimal amount value",
  "token_contract": "0x...",
  "token_symbol": "SYMBOL or empty",
  "amount_type": "transfer|swap_in|swap_out|fee|mint|burn|deposit|withdraw|other",
  "context": "where found (event name + parameter name)",
  "confidence": 0.85,
  "from_address": "0x... or empty",
  "to_address": "0x... or empty", 
  "event_contract": "0x... contract that emitted the event",
  "is_valid_token": false
}

EXAMPLE ANALYSIS:

Good Example - ERC20 Transfer:
[
  {
    "amount": "0x1bc16d674ec80000",
    "token_contract": "0xa0b86a33e6c6c67d3c6d3d6d3d6d3d6d3d6d3d6d",
    "token_symbol": "USDC",
    "amount_type": "transfer",
    "context": "Transfer event - value parameter",
    "confidence": 0.95,
    "from_address": "0x1234...",
    "to_address": "0x5678...",
    "event_contract": "0xa0b86a33e6c6c67d3c6d3d6d3d6d3d6d3d6d3d6d",
    "is_valid_token": false
  }
]

Good Example - DEX Swap:
[
  {
    "amount": "1000000000000000000",
    "token_contract": "0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2",
    "token_symbol": "WETH",
    "amount_type": "swap_in",
    "context": "Swap event - amount0In parameter",
    "confidence": 0.9,
    "from_address": "0xuser...",
    "to_address": "0xpool...",
    "event_contract": "0xpair_contract...",
    "is_valid_token": false
  },
  {
    "amount": "2500000000",
    "token_contract": "0xa0b86a33e6c6cd67d26c0a6c6cd67d26c0a6c6cd6",
    "token_symbol": "USDC", 
    "amount_type": "swap_out",
    "context": "Swap event - amount1Out parameter",
    "confidence": 0.9,
    "from_address": "0xpool...",
    "to_address": "0xuser...",
    "event_contract": "0xpair_contract...",
    "is_valid_token": false
  }
]

Be thorough but precise. Only include amounts where you can confidently identify the associated token contract and the amount represents actual value flow.`, contextData)
}

// validateTokenContracts validates that detected token contracts are real ERC20 tokens
func (a *AmountsFinder) validateTokenContracts(amounts []DetectedAmount, baggage map[string]interface{}) []DetectedAmount {
	// Get known token metadata
	tokenMetadata, hasMetadata := baggage["token_metadata"].(map[string]*TokenMetadata)

	// Get resolved contracts
	resolvedContracts, hasContracts := baggage["resolved_contracts"].(map[string]*ContractInfo)

	var validatedAmounts []DetectedAmount

	for _, amount := range amounts {
		// Validate token contract
		isValidToken := false

		// Check against known token metadata
		if hasMetadata {
			if metadata, exists := tokenMetadata[strings.ToLower(amount.TokenContract)]; exists {
				if metadata.Type == "ERC20" || metadata.Type == "Token" {
					isValidToken = true
				}
			}
		}

		// Check against resolved contracts for ERC20 patterns
		if !isValidToken && hasContracts {
			if contract, exists := resolvedContracts[strings.ToLower(amount.TokenContract)]; exists {
				// Look for ERC20-like methods in ABI
				hasTransfer := false
				hasBalanceOf := false
				for _, method := range contract.ParsedABI {
					if method.Type == "function" {
						if method.Name == "transfer" {
							hasTransfer = true
						}
						if method.Name == "balanceOf" {
							hasBalanceOf = true
						}
					}
				}
				if hasTransfer && hasBalanceOf {
					isValidToken = true
				}
			}
		}

		// Apply validation result
		amount.IsValidToken = isValidToken

		// Only include validated tokens or high-confidence amounts
		if isValidToken || amount.Confidence >= 0.8 {
			validatedAmounts = append(validatedAmounts, amount)
		}
	}

	return validatedAmounts
}

// GetPromptContext provides amounts context for LLM prompts
func (a *AmountsFinder) GetPromptContext(ctx context.Context, baggage map[string]interface{}) string {
	detectedAmounts, ok := baggage["detected_amounts"].([]DetectedAmount)
	if !ok || len(detectedAmounts) == 0 {
		return ""
	}

	var contextParts []string
	contextParts = append(contextParts, "### DETECTED AMOUNTS:")

	for i, amount := range detectedAmounts {
		amountInfo := fmt.Sprintf("Amount #%d:", i+1)
		amountInfo += fmt.Sprintf("\n- Value: %s", amount.Amount)
		amountInfo += fmt.Sprintf("\n- Token: %s", amount.TokenContract)
		if amount.TokenSymbol != "" {
			amountInfo += fmt.Sprintf(" (%s)", amount.TokenSymbol)
		}
		amountInfo += fmt.Sprintf("\n- Type: %s", amount.AmountType)
		amountInfo += fmt.Sprintf("\n- Context: %s", amount.Context)
		amountInfo += fmt.Sprintf("\n- Confidence: %.1f%%", amount.Confidence*100)

		if amount.FromAddress != "" {
			amountInfo += fmt.Sprintf("\n- From: %s", amount.FromAddress)
		}
		if amount.ToAddress != "" {
			amountInfo += fmt.Sprintf("\n- To: %s", amount.ToAddress)
		}

		validationStatus := "❌ Unvalidated"
		if amount.IsValidToken {
			validationStatus = "✅ Validated Token"
		}
		amountInfo += fmt.Sprintf("\n- Validation: %s", validationStatus)

		contextParts = append(contextParts, amountInfo)
	}

	return strings.Join(contextParts, "\n\n")
}

// GetRagContext provides RAG context for detected amounts (minimal for this tool)
func (a *AmountsFinder) GetRagContext(ctx context.Context, baggage map[string]interface{}) *RagContext {
	ragContext := NewRagContext()
	// Amounts finder processes transaction-specific amount data
	// No general knowledge to contribute to RAG
	return ragContext
}
