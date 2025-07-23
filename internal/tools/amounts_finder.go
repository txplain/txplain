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
	llm     llms.Model
	verbose bool
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
		llm:     llm,
		verbose: false,
	}
}

// SetVerbose enables or disables verbose logging
func (a *AmountsFinder) SetVerbose(verbose bool) {
	a.verbose = verbose
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
	if a.verbose {
		fmt.Println("\n" + strings.Repeat("💰", 60))
		fmt.Println("🔍 AMOUNTS FINDER: Starting AI-powered amount detection")
		fmt.Println(strings.Repeat("💰", 60))
	}

	// Get progress tracker from baggage for sub-progress updates
	var progressTracker *models.ProgressTracker
	if tracker, ok := baggage["progress_tracker"].(*models.ProgressTracker); ok {
		progressTracker = tracker
	}

	// Sub-step 1: Building analysis context
	if progressTracker != nil {
		progressTracker.UpdateComponent(
			"amounts_finder",
			models.ComponentGroupEnrichment,
			"Detecting Transaction Amounts",
			models.ComponentStatusRunning,
			"Building comprehensive analysis context...",
		)
	}

	if a.verbose {
		fmt.Println("📋 Sub-step 1: Building comprehensive analysis context...")
	}

	// Build comprehensive context for the LLM
	contextData := a.buildAnalysisContext(baggage)

	if a.verbose {
		fmt.Printf("📊 Built analysis context: %d characters\n", len(contextData))
	}

	// Sub-step 2: LLM Analysis (the time-consuming part)
	if progressTracker != nil {
		progressTracker.UpdateComponent(
			"amounts_finder",
			models.ComponentGroupEnrichment,
			"Detecting Transaction Amounts",
			models.ComponentStatusRunning,
			"Analyzing transaction with AI to identify amounts...",
		)
	}

	if a.verbose {
		fmt.Println("🧠 Sub-step 2: Analyzing transaction with AI to identify amounts...")
	}

	// Use LLM to identify all amounts
	detectedAmounts, err := a.identifyAmountsWithLLM(ctx, contextData)
	if err != nil {
		if a.verbose {
			fmt.Printf("❌ LLM analysis failed: %v\n", err)
			fmt.Println(strings.Repeat("💰", 60) + "\n")
		}
		return fmt.Errorf("failed to identify amounts with LLM: %w", err)
	}

	if a.verbose {
		fmt.Printf("🧠 LLM detected %d potential amounts\n", len(detectedAmounts))
	}

	// Sub-step 3: Validation
	if progressTracker != nil {
		progressTracker.UpdateComponent(
			"amounts_finder",
			models.ComponentGroupEnrichment,
			"Detecting Transaction Amounts",
			models.ComponentStatusRunning,
			"Validating detected amounts against token contracts...",
		)
	}

	if a.verbose {
		fmt.Println("✅ Sub-step 3: Validating detected amounts against token contracts...")
	}

	// Validate token contracts and filter results
	validatedAmounts := a.validateTokenContracts(detectedAmounts, baggage)

	if a.verbose {
		fmt.Printf("✅ Validated %d amounts after filtering\n", len(validatedAmounts))

		// Show summary of detected amounts
		if len(validatedAmounts) > 0 {
			fmt.Println("\n📋 DETECTED AMOUNTS SUMMARY:")
			for i, amount := range validatedAmounts {
				fmt.Printf("   %d. %s %s (%s) - Confidence: %.2f\n",
					i+1, amount.Amount, amount.TokenSymbol, amount.AmountType, amount.Confidence)
			}
		}

		fmt.Println("\n" + strings.Repeat("💰", 60))
		fmt.Println("✅ AMOUNTS FINDER: Completed successfully")
		fmt.Println(strings.Repeat("💰", 60) + "\n")
	}

	// Final progress update (will be automatically updated by pipeline to finished)
	if progressTracker != nil {
		progressTracker.UpdateComponent(
			"amounts_finder",
			models.ComponentGroupEnrichment,
			"Detecting Transaction Amounts",
			models.ComponentStatusRunning,
			"Amount detection completed successfully",
		)
	}

	if a.verbose {
		fmt.Println("🎯 Sub-step 4: Amount detection completed successfully")
	}

	// Add to baggage for downstream tools
	baggage["detected_amounts"] = validatedAmounts

	return nil
}

// buildAnalysisContext creates context using proper tool isolation - only using context providers
func (a *AmountsFinder) buildAnalysisContext(baggage map[string]interface{}) string {
	var contextParts []string

	// Use proper architecture: collect context from all context providers via GetPromptContext()
	if contextProviders, ok := baggage["context_providers"].([]interface{}); ok {
		for _, provider := range contextProviders {
			if toolProvider, ok := provider.(Tool); ok {
				if context := toolProvider.GetPromptContext(context.Background(), baggage); context != "" {
					contextParts = append(contextParts, context)
				}
			}
		}
	}

	// Add minimal raw transaction data that THIS tool needs for amount detection
	if rawData, ok := baggage["raw_data"].(map[string]interface{}); ok {
		if tx, ok := rawData["transaction"].(map[string]interface{}); ok {
			contextParts = append(contextParts, "\n### TRANSACTION DATA (for amount detection):")
			if value, ok := tx["value"].(string); ok && value != "0x0" {
				contextParts = append(contextParts, fmt.Sprintf("- Native token value: %s", value))
			}
			if input, ok := tx["input"].(string); ok && input != "0x" {
				contextParts = append(contextParts, fmt.Sprintf("- Input data: %s", input))
			}
		}

		// Add gas fee information for detection as an amount
		if receipt, ok := rawData["receipt"].(map[string]interface{}); ok {
			contextParts = append(contextParts, "\n### GAS FEE DATA (for gas fee amount detection):")

			// Gas used and effective gas price
			if gasUsed, ok := receipt["gasUsed"].(string); ok {
				contextParts = append(contextParts, fmt.Sprintf("- Gas Used: %s", gasUsed))
			}
			if effectiveGasPrice, ok := receipt["effectiveGasPrice"].(string); ok {
				contextParts = append(contextParts, fmt.Sprintf("- Effective Gas Price: %s", effectiveGasPrice))
			}

			// Network ID for native token identification
			if networkID, ok := rawData["network_id"].(float64); ok {
				contextParts = append(contextParts, fmt.Sprintf("- Network ID: %.0f", networkID))
			}

			// Transaction sender (who paid gas)
			if from, ok := receipt["from"].(string); ok {
				contextParts = append(contextParts, fmt.Sprintf("- Gas Paid By: %s", from))
			}

			contextParts = append(contextParts, "- Note: Gas fees should be detected as 'fee' type amounts using native token contract")
		}
	}

	return strings.Join(contextParts, "\n")
}

// identifyAmountsWithLLM uses LLM to identify all relevant amounts
func (a *AmountsFinder) identifyAmountsWithLLM(ctx context.Context, contextData string) ([]DetectedAmount, error) {
	prompt := a.buildAmountAnalysisPrompt(contextData)

	if a.verbose {
		fmt.Println("\n" + strings.Repeat("=", 80))
		fmt.Println("🤖 AMOUNTS FINDER: LLM PROMPT")
		fmt.Println(strings.Repeat("=", 80))
		fmt.Println(prompt)
		fmt.Println(strings.Repeat("=", 80))
		fmt.Println()
	}

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

	if a.verbose {
		fmt.Println(strings.Repeat("=", 80))
		fmt.Println("🤖 AMOUNTS FINDER: LLM RESPONSE")
		fmt.Println(strings.Repeat("=", 80))
		fmt.Println(responseText)
		fmt.Println(strings.Repeat("=", 80) + "\n")
	}

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
   - Gas fees (calculate from gasUsed × effectiveGasPrice for native token amounts)
   - Any hex or decimal numbers that could represent token amounts

2. **ASSOCIATE WITH TOKENS**: For each amount, determine:
   - The token contract address that this amount refers to
   - The token symbol if available from the context
   - Whether this is a known ERC20 token based on available metadata
   - For gas fees, use "native" as token_contract and derive symbol from network (ETH for Ethereum, etc.)

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

4. **GAS FEE DETECTION**: ALWAYS detect gas fees when gasUsed and effectiveGasPrice are available:
   - Calculate total gas fee: gasUsed × effectiveGasPrice (result in wei for Ethereum)
   - Use amount_type: "fee"
   - Use token_contract: "native" 
   - Use token_symbol based on network: "ETH" for network 1, "MATIC" for 137, "BNB" for 56, etc.
   - Use from_address: the transaction sender (who paid the gas)
   - Context: "Gas fee - calculated from gasUsed × effectiveGasPrice"
   - Confidence: 1.0 (gas fees are always certain)

5. **VALIDATION RULES**:
   - Only include amounts that have clear token contract associations OR are gas fees
   - Skip amounts of zero or empty values
   - Skip raw transaction data that doesn't represent token amounts
   - Focus on meaningful amounts that represent actual value transfers
   - ALWAYS include gas fees when gas data is available

6. **CONFIDENCE SCORING**:
   - 1.0: Gas fees and verified amounts with clear contract associations
   - 0.9-1.0: Clear token contract + verified metadata + explicit amount parameter
   - 0.7-0.9: Clear token contract + amount parameter, but less context
   - 0.5-0.7: Inferred token contract + probable amount
   - 0.3-0.5: Uncertain token association or amount interpretation
   - Below 0.3: Don't include

OUTPUT FORMAT:
Respond with a JSON array of detected amounts. Each should include:

{
  "amount": "hex or decimal amount value",
  "token_contract": "FULL CONTRACT ADDRESS (0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48) or 'native' for gas fees - NEVER use shortened addresses",
  "token_symbol": "SYMBOL or empty",
  "amount_type": "transfer|swap_in|swap_out|fee|mint|burn|deposit|withdraw|other",
  "context": "where found (event name + parameter name)",
  "confidence": 0.85,
  "from_address": "FULL ADDRESS (0x55fe002aeff02f77364de339a1292923a15844b8) or empty - NEVER use shortened addresses",
  "to_address": "FULL ADDRESS (0x0000000000000000000000000000000000000000) or empty - NEVER use shortened addresses", 
  "event_contract": "FULL CONTRACT ADDRESS that emitted the event - NEVER use shortened addresses",
  "is_valid_token": false
}

CRITICAL RULE FOR JSON OUTPUT:
- Always use FULL 42-CHARACTER contract addresses in the JSON output (0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48)
- NEVER use shortened addresses (0xa0b8...eb48) in the JSON token_contract, from_address, to_address, or event_contract fields  
- The Address Formatting Guide is ONLY for explanatory text - JSON output must use full addresses
- If you see a shortened address in the context, use the full address from the mapping above

EXAMPLE ANALYSIS:

Good Example - ERC20 Transfer:
[
  {
    "amount": "0x1bc16d674ec80000",
    "token_contract": "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48",
    "token_symbol": "USDC",
    "amount_type": "transfer",
    "context": "Transfer event - value parameter",
    "confidence": 0.95,
    "from_address": "0x55fe002aeff02f77364de339a1292923a15844b8",
    "to_address": "0x0000000000000000000000000000000000000000",
    "event_contract": "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48",
    "is_valid_token": false
  }
]

Good Example - Gas Fee:
[
  {
    "amount": "21000000000000000",
    "token_contract": "native",
    "token_symbol": "ETH",
    "amount_type": "fee",
    "context": "Gas fee - calculated from gasUsed × effectiveGasPrice",
    "confidence": 1.0,
    "from_address": "0x55fe002aeff02f77364de339a1292923a15844b8",
    "to_address": "",
    "event_contract": "",
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
    "from_address": "0x55fe002aeff02f77364de339a1292923a15844b8",
    "to_address": "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48",
    "event_contract": "0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2",
    "is_valid_token": false
  },
  {
    "amount": "2500000000",
    "token_contract": "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48",
    "token_symbol": "USDC", 
    "amount_type": "swap_out",
    "context": "Swap event - amount1Out parameter",
    "confidence": 0.9,
    "from_address": "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48",
    "to_address": "0x55fe002aeff02f77364de339a1292923a15844b8",
    "event_contract": "0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2",
    "is_valid_token": false
  }
]

Be thorough but precise. Only include amounts where you can confidently identify the associated token contract and the amount represents actual value flow. ALWAYS include gas fees when gas data is available.`, contextData)
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
