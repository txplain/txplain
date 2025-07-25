package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
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
func NewAmountsFinder(llm llms.Model, verbose bool) *AmountsFinder {
	return &AmountsFinder{
		llm:     llm,
		verbose: verbose,
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

// calculateGasFee calculates the gas fee programmatically from hex values
func (a *AmountsFinder) calculateGasFee(baggage map[string]interface{}) *DetectedAmount {
	rawData, ok := baggage["raw_data"].(map[string]interface{})
	if !ok {
		return nil
	}

	receipt, ok := rawData["receipt"].(map[string]interface{})
	if !ok {
		return nil
	}

	// Get gas used and effective gas price as hex strings
	gasUsedStr, hasGasUsed := receipt["gasUsed"].(string)
	effectiveGasPriceStr, hasEffectiveGasPrice := receipt["effectiveGasPrice"].(string)

	if !hasGasUsed || !hasEffectiveGasPrice {
		return nil
	}

	// Convert hex to big.Int for precise calculation
	gasUsed := new(big.Int)
	effectiveGasPrice := new(big.Int)

	// Parse hex values (remove 0x prefix if present)
	gasUsedHex := strings.TrimPrefix(gasUsedStr, "0x")
	effectiveGasPriceHex := strings.TrimPrefix(effectiveGasPriceStr, "0x")

	if _, ok := gasUsed.SetString(gasUsedHex, 16); !ok {
		return nil
	}
	if _, ok := effectiveGasPrice.SetString(effectiveGasPriceHex, 16); !ok {
		return nil
	}

	// Calculate gas fee: gasUsed * effectiveGasPrice
	gasFeeWei := new(big.Int).Mul(gasUsed, effectiveGasPrice)

	if a.verbose {
		fmt.Printf("🔍 GAS_FEE_DEBUG: gasUsed=%s, effectiveGasPrice=%s, gasFeeWei=%s\n",
			gasUsed.String(), effectiveGasPrice.String(), gasFeeWei.String())
	}

	// Get transaction sender who paid the gas
	fromAddress := ""
	if from, ok := receipt["from"].(string); ok {
		fromAddress = from
	}

	// Get network ID for native token symbol
	networkID, ok := rawData["network_id"].(float64)
	if !ok {
		return nil
	}

	// Determine native token symbol based on network
	var tokenSymbol string
	switch int64(networkID) {
	case 1:
		tokenSymbol = "ETH"
	case 56:
		tokenSymbol = "BNB"
	case 137:
		tokenSymbol = "MATIC"
	case 10:
		tokenSymbol = "ETH"
	case 42161:
		tokenSymbol = "ETH"
	default:
		tokenSymbol = "ETH" // Default to ETH
	}

	return &DetectedAmount{
		Amount:        gasFeeWei.String(),
		TokenContract: "native",
		TokenSymbol:   tokenSymbol,
		AmountType:    "fee",
		Context:       "Gas fee - calculated programmatically from gasUsed × effectiveGasPrice",
		Confidence:    1.0,
		FromAddress:   fromAddress,
		ToAddress:     "",
		EventContract: "",
		IsValidToken:  false,
	}
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

	// Sub-step 1: Calculate gas fee programmatically (before AI analysis)
	if progressTracker != nil {
		progressTracker.UpdateComponent(
			"amounts_finder",
			models.ComponentGroupEnrichment,
			"Detecting Transaction Amounts",
			models.ComponentStatusRunning,
			"Calculating gas fees...",
		)
	}

	var detectedAmounts []DetectedAmount

	// Calculate gas fee programmatically to avoid AI math errors
	if gasFee := a.calculateGasFee(baggage); gasFee != nil {
		detectedAmounts = append(detectedAmounts, *gasFee)
		if a.verbose {
			fmt.Println("⛽ Pre-calculated gas fee programmatically")
		}
	}

	// Sub-step 2: Building analysis context
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
		fmt.Println("📋 Sub-step 2: Building comprehensive analysis context...")
	}

	// Build comprehensive context for the LLM
	contextData := a.buildAnalysisContext(baggage)

	if a.verbose {
		fmt.Printf("📊 Built analysis context: %d characters\n", len(contextData))
	}

	// Sub-step 3: LLM Analysis (the time-consuming part)
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
		fmt.Println("🧠 Sub-step 3: Analyzing transaction with AI to identify amounts...")
	}

	// Use LLM to identify all amounts EXCEPT gas fees (which we already calculated)
	llmDetectedAmounts, err := a.identifyAmountsWithLLM(ctx, contextData)
	if err != nil {
		// Update progress tracker to show the error before returning
		if progressTracker != nil {
			progressTracker.UpdateComponent(
				"amounts_finder",
				models.ComponentGroupEnrichment,
				"Detecting Transaction Amounts",
				models.ComponentStatusRunning,
				fmt.Sprintf("AI analysis failed: %v", err),
			)
		}
		return fmt.Errorf("failed to identify amounts with LLM: %w", err)
	}

	// Filter out any AI-detected gas fees since we calculated them programmatically
	for _, amount := range llmDetectedAmounts {
		if amount.AmountType != "fee" {
			detectedAmounts = append(detectedAmounts, amount)
		} else if a.verbose {
			fmt.Println("🔍 Filtered out AI-detected gas fee (using programmatic calculation instead)")
		}
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

	// Call LLM with retry logic for robustness against context cancellation and network errors
	response, err := CallLLMWithRetry(ctx, a.llm, []llms.MessageContent{
		{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{
				llms.TextPart(prompt),
			},
		},
	}, a.verbose)
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

	// Extract JSON from response (handle potential markdown formatting and extra text)
	jsonStr, err := a.extractJSONArray(responseText)
	if err != nil {
		if a.verbose {
			fmt.Printf("❌ Failed to extract JSON from response: %v\n", err)
			fmt.Printf("Raw response was: %s\n", responseText)
		}
		return nil, fmt.Errorf("failed to extract JSON from response: %w", err)
	}

	if err := json.Unmarshal([]byte(jsonStr), &amounts); err != nil {
		if a.verbose {
			fmt.Printf("❌ Failed to parse extracted JSON: %v\n", err)
			fmt.Printf("Extracted JSON was: %s\n", jsonStr)
		}
		return nil, fmt.Errorf("failed to parse LLM response: %w", err)
	}

	return amounts, nil
}

// extractJSONArray extracts a JSON array from the LLM response, handling markdown code blocks and extra text
func (a *AmountsFinder) extractJSONArray(responseText string) (string, error) {
	// Remove any markdown code block formatting
	responseText = strings.ReplaceAll(responseText, "```json", "")
	responseText = strings.ReplaceAll(responseText, "```", "")

	// Find the start of the JSON array
	jsonStart := strings.Index(responseText, "[")
	if jsonStart == -1 {
		return "", fmt.Errorf("no JSON array found in response")
	}

	// Find the matching closing bracket for the array
	// We need to properly balance brackets to avoid including extra text after the JSON
	bracketCount := 0
	jsonEnd := -1
	inString := false
	escapeNext := false

	for i := jsonStart; i < len(responseText); i++ {
		char := responseText[i]

		if escapeNext {
			escapeNext = false
			continue
		}

		if char == '\\' {
			escapeNext = true
			continue
		}

		if char == '"' {
			inString = !inString
			continue
		}

		if !inString {
			switch char {
			case '[':
				bracketCount++
			case ']':
				bracketCount--
				if bracketCount == 0 {
					jsonEnd = i
					break
				}
			}
		}

		if jsonEnd != -1 {
			break
		}
	}

	if jsonEnd == -1 {
		return "", fmt.Errorf("incomplete JSON array - no matching closing bracket found")
	}

	jsonStr := responseText[jsonStart : jsonEnd+1]

	// Validate that it's actually valid JSON by attempting to parse it as interface{}
	var testParse interface{}
	if err := json.Unmarshal([]byte(jsonStr), &testParse); err != nil {
		return "", fmt.Errorf("extracted text is not valid JSON: %w", err)
	}

	return jsonStr, nil
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
   
   Note: Gas fees are calculated programmatically and do not need to be detected by the AI.

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

4. **VALIDATION RULES**:
   - Only include amounts that have clear token contract associations
   - Skip amounts of zero or empty values
   - Skip raw transaction data that doesn't represent token amounts
   - Focus on meaningful amounts that represent actual value transfers

5. **CONFIDENCE SCORING**:
   - 1.0: Verified amounts with clear contract associations
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

Be thorough but precise. Only include amounts where you can confidently identify the associated token contract and the amount represents actual value flow.

*** CRITICAL RESPONSE FORMAT REQUIREMENT ***

Your response MUST contain ONLY raw JSON - no explanations, no markdown formatting, no code blocks, no additional text before or after the JSON array.

BAD - Do NOT include any of these:
- "Here's the analysis:"
- "`+"```json"+`"
- "`+"```"+`"
- "The detected amounts are:"
- Any explanatory text
- Any comments outside the JSON

GOOD - Your entire response should look exactly like this:
[{"amount":"0x1bc16d674ec80000","token_contract":"0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48","token_symbol":"USDC","amount_type":"transfer","context":"Transfer event - value parameter","confidence":0.95,"from_address":"0x55fe002aeff02f77364de339a1292923a15844b8","to_address":"0x0000000000000000000000000000000000000000","event_contract":"0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48","is_valid_token":false}]

RESPOND WITH RAW JSON ARRAY ONLY!`, contextData)
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
