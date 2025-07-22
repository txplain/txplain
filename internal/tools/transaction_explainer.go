package tools

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"os"

	"github.com/tmc/langchaingo/llms"
	"github.com/txplain/txplain/internal/models"
)

// TransactionExplainer generates human-readable explanations from decoded transaction data
type TransactionExplainer struct {
	llm     llms.Model
	verbose bool
}

// NewTransactionExplainer creates a new TransactionExplainer tool
func NewTransactionExplainer(llm llms.Model) *TransactionExplainer {
	return &TransactionExplainer{
		llm:     llm,
		verbose: false,
	}
}

// SetVerbose enables or disables verbose logging
func (t *TransactionExplainer) SetVerbose(verbose bool) {
	t.verbose = verbose
}

// Dependencies returns the tools this processor depends on
func (t *TransactionExplainer) Dependencies() []string {
	return []string{"abi_resolver", "log_decoder", "token_transfer_extractor", "nft_decoder", "token_metadata_enricher", "erc20_price_lookup", "monetary_value_enricher", "ens_resolver", "protocol_resolver"}
}

// Process generates explanation using all information from baggage
func (t *TransactionExplainer) Process(ctx context.Context, baggage map[string]interface{}) error {
	// Add decoded data and raw data to baggage for generateExplanationWithBaggage
	if events, ok := baggage["events"].([]models.Event); ok {
		decodedData := &models.DecodedData{
			Events: events,
			// Calls would come from trace decoder if implemented
		}
		baggage["decoded_data"] = decodedData
	}

	// Collect context from all context providers in the baggage
	var additionalContext []string
	if contextProviders, ok := baggage["context_providers"].([]ContextProvider); ok {
		for _, provider := range contextProviders {
			if context := provider.GetPromptContext(ctx, baggage); context != "" {
				additionalContext = append(additionalContext, context)
			}
		}
	}

	// Generate explanation with context from other tools
	explanation, err := t.generateExplanationWithBaggage(ctx, baggage, additionalContext)
	if err != nil {
		return fmt.Errorf("failed to generate explanation: %w", err)
	}

	// Add explanation to baggage
	baggage["explanation"] = explanation
	return nil
}

// Name returns the tool name
func (t *TransactionExplainer) Name() string {
	return "transaction_explainer"
}

// Description returns the tool description
func (t *TransactionExplainer) Description() string {
	return "Generates human-readable explanations of blockchain transactions from decoded calls and events"
}

// Run executes the transaction explanation
func (t *TransactionExplainer) Run(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
	// Extract decoded data
	decodedData, err := t.extractDecodedData(input)
	if err != nil {
		return nil, NewToolError("transaction_explainer", fmt.Sprintf("failed to extract decoded data: %v", err), "INVALID_INPUT")
	}

	// Extract raw transaction data for additional context
	rawData, _ := input["raw_data"].(map[string]interface{})

	// Generate explanation using LLM
	explanation, err := t.generateExplanationWithContext(ctx, decodedData, rawData, []string{})
	if err != nil {
		return nil, NewToolError("transaction_explainer", fmt.Sprintf("failed to generate explanation: %v", err), "LLM_ERROR")
	}

	return map[string]interface{}{
		"explanation": explanation,
	}, nil
}

// extractDecodedData extracts and validates decoded transaction data from input
func (t *TransactionExplainer) extractDecodedData(input map[string]interface{}) (*models.DecodedData, error) {
	data := &models.DecodedData{}

	// Extract calls
	if callsInterface, ok := input["calls"].([]interface{}); ok {
		for _, callInterface := range callsInterface {
			if callMap, ok := callInterface.(map[string]interface{}); ok {
				call := models.Call{}

				if contract, ok := callMap["contract"].(string); ok {
					call.Contract = contract
				}
				if method, ok := callMap["method"].(string); ok {
					call.Method = method
				}
				if callType, ok := callMap["call_type"].(string); ok {
					call.CallType = callType
				}
				if value, ok := callMap["value"].(string); ok {
					call.Value = value
				}
				if gasUsed, ok := callMap["gas_used"].(float64); ok {
					call.GasUsed = uint64(gasUsed)
				}
				if success, ok := callMap["success"].(bool); ok {
					call.Success = success
				}
				if errorReason, ok := callMap["error_reason"].(string); ok {
					call.ErrorReason = errorReason
				}
				if depth, ok := callMap["depth"].(float64); ok {
					call.Depth = int(depth)
				}
				if args, ok := callMap["arguments"].(map[string]interface{}); ok {
					call.Arguments = args
				}

				data.Calls = append(data.Calls, call)
			}
		}
	}

	// Extract events - handle both []interface{} and []models.Event
	if eventsInterface, ok := input["events"]; ok {
		// Handle []models.Event (direct type)
		if eventsList, ok := eventsInterface.([]models.Event); ok {
			data.Events = eventsList
		} else if eventsInterface, ok := eventsInterface.([]interface{}); ok {
			// Handle []interface{} (legacy format)
			for _, eventInterface := range eventsInterface {
				if eventMap, ok := eventInterface.(map[string]interface{}); ok {
					event := models.Event{}

					if contract, ok := eventMap["contract"].(string); ok {
						event.Contract = contract
					}
					if name, ok := eventMap["name"].(string); ok {
						event.Name = name
					}
					if params, ok := eventMap["parameters"].(map[string]interface{}); ok {
						event.Parameters = params
					}
					if topics, ok := eventMap["topics"].([]interface{}); ok {
						for _, topic := range topics {
							if topicStr, ok := topic.(string); ok {
								event.Topics = append(event.Topics, topicStr)
							}
						}
					}
					if data, ok := eventMap["data"].(string); ok {
						event.Data = data
					}

					data.Events = append(data.Events, event)
				}
			}
		}
	}

	return data, nil
}

// generateExplanationWithBaggage uses the LLM to create a human-readable explanation with additional context from baggage
func (t *TransactionExplainer) generateExplanationWithBaggage(ctx context.Context, baggage map[string]interface{}, additionalContext []string) (*models.ExplanationResult, error) {
	// Extract data from baggage
	decodedData, _ := baggage["decoded_data"].(*models.DecodedData)
	rawData, _ := baggage["raw_data"].(map[string]interface{})

	// If no decoded data, create empty structure
	if decodedData == nil {
		decodedData = &models.DecodedData{}
	}

	// Generate explanation using the main method
	explanation, err := t.generateExplanationWithContext(ctx, decodedData, rawData, additionalContext)
	if err != nil {
		return nil, err
	}

	// Update the result with baggage-specific data (transfers from TokenTransferExtractor)
	if transfers, ok := baggage["transfers"].([]models.TokenTransfer); ok {
		// Filter out any remaining empty or invalid transfers
		var validTransfers []models.TokenTransfer
		for _, transfer := range transfers {
			// Only include transfers with valid data
			if transfer.From != "" && transfer.To != "" && 
			   transfer.From != "0x" && transfer.To != "0x" &&
			   len(transfer.From) >= 10 && len(transfer.To) >= 10 {
				validTransfers = append(validTransfers, transfer)
			}
		}
		
		explanation.Transfers = validTransfers
	}

	return explanation, nil
}

// generateExplanationWithContext uses the LLM to create a human-readable explanation with additional context
func (t *TransactionExplainer) generateExplanationWithContext(ctx context.Context, decodedData *models.DecodedData, rawData map[string]interface{}, additionalContext []string) (*models.ExplanationResult, error) {
	// Build the prompt with additional context
	prompt := t.buildExplanationPrompt(decodedData, rawData, additionalContext)

	if t.verbose {
		fmt.Println("=== TRANSACTION EXPLAINER: PROMPT SENT TO LLM ===")
		fmt.Println(prompt)
		fmt.Println("=== END OF PROMPT ===")
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

	// Parse the LLM response and build the explanation result
	responseText := ""
	if response != nil && len(response.Choices) > 0 {
		responseText = response.Choices[0].Content
	}

	if t.verbose {
		fmt.Println("=== TRANSACTION EXPLAINER: LLM RESPONSE ===")
		fmt.Println(responseText)
		fmt.Println("=== END OF LLM RESPONSE ===")
		fmt.Println()
	}

	explanation := t.parseExplanationResponse(responseText, decodedData, rawData)

	return explanation, nil
}



// buildExplanationPrompt creates the prompt for the LLM
func (t *TransactionExplainer) buildExplanationPrompt(decodedData *models.DecodedData, rawData map[string]interface{}, additionalContexts []string) string {
	prompt := `You are a blockchain transaction analyzer. Provide a VERY SHORT, precise summary of what this transaction accomplished. Keep it under 30 words - users have only 2 seconds to read.

Focus ONLY on the main action. Don't explain blockchain basics or add warnings.

## Decoded Transaction Data:

### Function Calls:`

	// Add calls information
	for i, call := range decodedData.Calls {
		prompt += fmt.Sprintf(`

Call #%d:
- Contract: %s
- Method: %s
- Call Type: %s
- Value: %s ETH
- Gas Used: %d
- Success: %t`,
			i+1, call.Contract, call.Method, call.CallType,
			t.weiToEther(call.Value), call.GasUsed, call.Success)

		if call.ErrorReason != "" {
			prompt += fmt.Sprintf(`
- Error: %s`, call.ErrorReason)
		}

		if len(call.Arguments) > 0 {
			prompt += `
- Arguments:`
			for key, value := range call.Arguments {
				prompt += fmt.Sprintf(`
  - %s: %v`, key, value)
			}
		}
	}

	prompt += `

### Events Emitted:`

	// Add events information
	for i, event := range decodedData.Events {
		prompt += fmt.Sprintf(`

Event #%d:
- Contract: %s
- Event: %s
- Topics: %v`,
			i+1, event.Contract, event.Name, event.Topics)

		if len(event.Parameters) > 0 {
			prompt += `
- Parameters:`
			for key, value := range event.Parameters {
				prompt += fmt.Sprintf(`
  - %s: %v`, key, value)
			}
		}
	}

	// Token transfers will be included via Additional Context from TokenTransferExtractor

	// Add raw transaction context if available
	if rawData != nil {
		if receipt, ok := rawData["receipt"].(map[string]interface{}); ok {
			if gasUsed, ok := receipt["gasUsed"].(string); ok {
				prompt += fmt.Sprintf(`

### Transaction Context:
- Total Gas Used: %s`, gasUsed)
			}
			if status, ok := receipt["status"].(string); ok {
				prompt += fmt.Sprintf(`
- Status: %s`, t.formatStatus(status))
			}
		}
	}

	// Add additional context from tools
	for _, additionalContext := range additionalContexts {
		prompt += `

### Additional Context:
` + additionalContext
	}

	prompt += `

## Instructions:

Write a single, short sentence (under 30 words) describing the main action. 

MINTING DETECTION:
- When NFTs are minted from zero address (0x0000000000000000000000000000000000000000), use "minted" language
- The recipients are the addresses that RECEIVE the NFTs, not the payer
- For minting: "Minted X NFTs for Y USDC paid by payer-address to recipients"
- Use payment flow analysis to identify the actual payer and recipients correctly
- Multiple recipients should be handled: "Minted NFTs to 2 recipients for X USDC"

TRANSACTION TYPE DETECTION:
- When NFTs are involved alongside token transfers, prioritize describing it as a purchase/mint/trade rather than a swap
- Look for patterns: User pays tokens → receives NFTs = PURCHASE
- Look for patterns: User pays tokens → NFTs minted to recipients = MINTING
- Look for patterns: User pays tokens → receives NFTs + change = PURCHASE with change
- Look for patterns: User sends Token A → receives Token B (no NFTs) = SWAP
- PURCHASE examples: "Purchased 2 NFTs for 30 USDC", "Minted 5 NFTs from collection for 0.1 ETH"
- MINTING examples: "Minted 3,226x PAYKEN tokens for 30.32 USDC to 2 recipients"
- Avoid describing NFT transactions as "swaps" unless it's actually NFT-to-NFT trading

RECIPIENT IDENTIFICATION:
- For MINTING: Recipients are those who receive the newly minted NFTs
- For PURCHASES: Recipients are those who receive the purchased NFTs
- Use Payment Flow Analysis section to identify the correct payer
- Use NFT Transfers section to identify the correct recipients
- Don't confuse the payer with the recipients

MULTI-HOP SWAP DETECTION:
- For complex transactions with multiple token transfers, focus on the NET EFFECT for the user
- Look for the pattern: User sends Token A → Multiple intermediary transfers → User receives Token B
- The user is typically the address that appears in both the FIRST "from" and FINAL "to" positions across all transfers
- Ignore intermediary router/contract addresses that facilitate the swap
- CRITICAL: The final output token is what the user ultimately receives, NOT intermediate tokens like WETH
- When WETH appears in transfers, it's usually an intermediary step - look for the final non-WETH token received by the user
- Trace through ALL transfers to find what the user actually ends up with after all conversions
- Example: User sends USDT → Router converts to WETH → Router converts WETH to GrowAI → User receives GrowAI
- In this case, report "Swapped USDT for GrowAI tokens" NOT "Swapped USDT for WETH"

USER IDENTIFICATION IN DEFI:
- CRITICAL: Always check for "ACTUAL USER" in Payment Flow Analysis section
- When ACTUAL USER is provided, use that address, NOT the contract/router addresses
- DeFi transactions often use intermediary contracts - show the real beneficiary
- Format examples:
  - "Repaid 25 USDC debt on Morpho for 0xea7b...1889 + $0.55 gas"
  - "Borrowed 100 DAI on Aave for 0x1234...5678 + $2.30 gas"
  - "Supplied 50 USDC to Compound for 0x9876...4321 + $1.20 gas"
- NEVER say "by 0xbbbb...ffcb" when the actual user is "0xea7b...1889"
- The contract address is the intermediary, not the user

PAYMENT FLOW ANALYSIS:
- Use the "Payment Flow Analysis" section to identify:
  - Who paid (the payer)
  - How much was paid initially 
  - How much reached the final recipient
  - What fees were deducted
  - CRITICAL: Who is the ACTUAL USER vs intermediary contracts
- CRITICAL: Always include fee information from "Fee Summary for Final Explanation" section
- Use suggested fee formats provided in the context
- For minting with fees: "Minted NFTs for 30.32 USDC (3.55 USDC fees + $0.85 gas)"
- For purchases with change: "Purchased NFTs for ~27.55 USDC net cost + $1.20 gas"
- Never omit fee information - users must know where all their value went

MANDATORY FEE REQUIREMENTS:
- ALWAYS include transaction fees when they exist (shown in Payment Flow Analysis)
- ALWAYS include gas fees when they exist (shown in Fee Summary)
- ALWAYS include fee recipient addresses when available (shown in Fee recipients section)
- Format examples:
  - "for 30.32 USDC (3.55 USDC fees to 0x0705...64e0 + $0.85 gas)"
  - "for 30.32 USDC (3.55 USDC fees to 3 recipients + $0.85 gas)"
  - "for 50 ETH + $12.50 gas"  
  - "for 100 USDT (2.5 USDT fees to 0x1234...5678)"
- Use "Fee Summary for Final Explanation" for exact formatting suggestions
- Show total cost to user when provided: "total cost $32.17"
- Fee recipients provide transparency about where fees went - include when available

IMPORTANT: 
- Use enriched monetary values from "Enriched Token Transfers" section when available (FormattedAmount and USD Value fields).
- Prefer specific USD values from the enriched data over raw hex values or basic token prices.
- If enriched transfers show "Amount: 43.94 ATH" and "USD Value: $1.45", use those exact values.
- Only fall back to raw data or basic prices if enriched values are not available.
- Always use the total converted amount, not the base unit price.
- Use the address formatting provided in the "Address Formatting Guide" section.
- Follow the address usage instructions from the ENS Names section.
- CRITICAL: Always include gas fees and transaction fees from the "Fee Summary" section
- Gas fees should be shown in USD format: "$0.85 gas", "$12.50 gas"

PROTOCOL USAGE:
- Always include specific protocol/aggregator names when available from the "Protocol Detection" section
- Use specific protocol names like "1inch v6", "Uniswap v3", "Curve", etc. instead of generic terms
- For aggregators, mention the aggregator name (e.g., "via 1inch aggregator", "through Paraswap")
- For DEX protocols, include the protocol name (e.g., "on Uniswap v3", "via Curve pool")

NFT HANDLING:
- Always include NFT transfers from the "NFT Transfers" section when present
- For ERC721 NFTs: mention the specific token ID and collection name
- For ERC1155 tokens: include both token ID and amount transferred - be specific about quantities
- Use collection names when available, otherwise use contract symbol or "Unknown NFT"
- Include NFT transfers in the main action summary alongside token transfers
- For large amounts, use clear formatting: "3,226 tokens of ID 3226" or "3,226x NFT #3226"
- CRITICAL: Always show explicit recipients and amounts - never use generic terms like "to 2 recipients"
- Use "Recipient Summary for Final Explanation" section to get specific recipient details
- For multiple recipients: show each recipient explicitly: "3,226x to 0x6686...1f28 and 3,226x to 0xd53c...eb47"
- For single recipients: show the recipient: "5 NFTs to 0x1234...5678"

EXPLICIT RECIPIENT REQUIREMENTS:
- NEVER say "to 2 recipients" or "to multiple recipients" - always show the specific addresses
- ALWAYS include the amount each recipient received
- Format: "3,226x PAYKEN tokens to 0x6686...1f28 and 3,226x to 0xd53c...eb47"
- Format: "1 CryptoPunk NFT #1234 to 0x1234...5678"
- Format: "5 BoredApes NFTs to 0x1111...2222, 3 to 0x3333...4444, and 2 to 0x5555...6666"
- Use the "Recipient Summary" section to get exact recipient addresses and amounts

Examples:
- "Minted 3,226x PAYKEN tokens (ID 3226) to 0x6686...1f28 and 3,226x to 0xd53c...eb47 for 30.32 USDC (3.55 USDC fees to 3 recipients + $0.18 gas)"
- "Minted 6,452x PAYKEN tokens for 30.32 USDC: 3,226x to 0x6686...1f28 and 3,226x to 0xd53c...eb47 (fees to 0x0705...64e0 + $0.25 gas)"
- "Purchased 2x NFT #3226 from PAYKEN collection for 30.32 USDC + $2.15 gas to 0x1234...5678"
- "Minted 5 BoredApes NFTs to 0x1111...2222 for 0.5 ETH + $45.80 gas"
- "Transferred 43.94 ATH ($1.45 USD) + $0.02 gas from one wallet to another"
- "Swapped 1 ETH for 2,485.75 USDT ($2,485.75 USD) on Uniswap v3 + $15.20 gas"  
- "Swapped 100 USDT ($100) for 57,071 GrowAI tokens via 1inch v6 aggregator + $3.45 gas"
- "Approved Uniswap v2 Router to spend unlimited DAI + $8.90 gas"
- "Transferred NFT #1234 from CryptoPunks collection + $12.50 gas"
- "Received 2 ERC-1155 tokens (ID 3226) from Tappers Kingdom + $1.25 gas"
- "Transferred 100 USDC from 0x1234...5678 (alice.eth) to 0x9876...4321 (bob.eth) + $0.85 gas"
- "Swapped 0.5 ETH for 1,250 USDC ($1,250) on Curve (0.1 ETH fees to 0x1234...5678 + $25.50 gas)"
- "Transferred 1,000 USDT ($1,000) paying 5 USDT fees to 0x9876...4321 + $2.30 gas via Paraswap"
- "Added liquidity to Uniswap v3 ETH/USDC pool + $18.75 gas"
- "Swapped 50 USDT for 0.02 WETH through SushiSwap router + $4.60 gas"

Be specific about amounts, tokens, protocols, and main action. No explanations or warnings.`

	return prompt
}

// parseExplanationResponse parses the LLM response and creates the result structure
func (t *TransactionExplainer) parseExplanationResponse(response string, decodedData *models.DecodedData, rawData map[string]interface{}) *models.ExplanationResult {
	result := &models.ExplanationResult{
		Summary:   response, // For now, use the full response as summary
		Transfers: []models.TokenTransfer{},
		Links:     make(map[string]string),
		Tags:      []string{},
		Metadata:  make(map[string]interface{}),
		Timestamp: time.Now(),
	}

	// Extract basic transaction info from raw data if available
	if rawData != nil {
		if networkID, ok := rawData["network_id"].(float64); ok {
			result.NetworkID = int64(networkID)
		}
		if txHash, ok := rawData["tx_hash"].(string); ok {
			result.TxHash = txHash
		}

		// Extract transaction details from receipt
		if receipt, ok := rawData["receipt"].(map[string]interface{}); ok {
			if gasUsed, ok := receipt["gasUsed"].(string); ok {
				if gas, err := strconv.ParseUint(gasUsed[2:], 16, 64); err == nil {
					result.GasUsed = gas
				}
			}
			if status, ok := receipt["status"].(string); ok {
				result.Status = t.formatStatus(status)
			}
			if blockNumber, ok := receipt["blockNumber"].(string); ok {
				if bn, err := strconv.ParseUint(blockNumber[2:], 16, 64); err == nil {
					result.BlockNumber = bn
				}
			}

			// Calculate and format transaction fee
			result.TxFee = t.calculateTransactionFee(receipt, result.NetworkID)
		}
	}

	// Token transfers should be provided via the Process method/baggage
	// For Run method, leave empty for now
	result.Transfers = []models.TokenTransfer{}

	// Generate tags based on transaction content
	result.Tags = t.generateTags(decodedData)

	// Generate links to explorers
	result.Links = t.generateLinks(result.TxHash, result.NetworkID, decodedData)

	return result
}



// generateTags creates tags based on transaction content
func (t *TransactionExplainer) generateTags(decodedData *models.DecodedData) []string {
	tags := []string{}

	// Add tags based on methods called
	for _, call := range decodedData.Calls {
		switch call.Method {
		case "transfer", "transferFrom":
			tags = append(tags, "token-transfer")
		case "approve":
			tags = append(tags, "token-approval")
		case "mint":
			tags = append(tags, "minting")
		case "swap", "swapExactETHForTokens", "swapExactTokensForTokens":
			tags = append(tags, "defi", "swap")
		case "addLiquidity", "addLiquidityETH":
			tags = append(tags, "defi", "liquidity")
		case "removeLiquidity", "removeLiquidityETH":
			tags = append(tags, "defi", "liquidity")
		}
	}

	// Add tags based on events
	for _, event := range decodedData.Events {
		switch event.Name {
		case "Transfer":
			tags = append(tags, "transfer")
		case "Swap":
			tags = append(tags, "defi", "swap")
		case "Mint", "Burn":
			tags = append(tags, "token-supply")
		}
	}

	// Remove duplicates
	tagMap := make(map[string]bool)
	var uniqueTags []string
	for _, tag := range tags {
		if !tagMap[tag] {
			tagMap[tag] = true
			uniqueTags = append(uniqueTags, tag)
		}
	}

	return uniqueTags
}

// generateLinks creates explorer links
func (t *TransactionExplainer) generateLinks(txHash string, networkID int64, decodedData *models.DecodedData) map[string]string {
	links := make(map[string]string)

	if txHash != "" && networkID > 0 {
		if network, exists := models.GetNetwork(networkID); exists {
			links["transaction"] = fmt.Sprintf("%s/tx/%s", network.Explorer, txHash)

			// Add contract links
			contracts := make(map[string]bool)
			for _, call := range decodedData.Calls {
				if call.Contract != "" {
					contracts[call.Contract] = true
				}
			}
			for _, event := range decodedData.Events {
				if event.Contract != "" {
					contracts[event.Contract] = true
				}
			}

			for contract := range contracts {
				links[contract] = fmt.Sprintf("%s/address/%s", network.Explorer, contract)
			}
		}
	}

	return links
}

// weiToEther converts wei string to ether string
func (t *TransactionExplainer) weiToEther(weiStr string) string {
	if weiStr == "" || weiStr == "0x" || weiStr == "0x0" {
		return "0"
	}

	// Remove 0x prefix if present
	if strings.HasPrefix(weiStr, "0x") {
		weiStr = weiStr[2:]
	}

	// Convert to big int
	wei, success := new(big.Int).SetString(weiStr, 16)
	if !success {
		return "0"
	}

	// Convert to ether (divide by 10^18)
	ether := new(big.Float).SetInt(wei)
	ether.Quo(ether, new(big.Float).SetFloat64(1e18))

	return ether.String()
}

// formatStatus formats transaction status
func (t *TransactionExplainer) formatStatus(status string) string {
	switch status {
	case "0x1":
		return "success"
	case "0x0":
		return "failed"
	default:
		return "unknown"
	}
}

// formatAmount formats token amounts from hex to decimal
func (t *TransactionExplainer) formatAmount(amount string, decimals int) string {
	if amount == "" {
		return "0"
	}

	// Try to convert hex amount to decimal
	if strings.HasPrefix(amount, "0x") {
		// Parse hex to big int
		if value, ok := new(big.Int).SetString(amount[2:], 16); ok {
			// Convert to float based on decimals
			if decimals > 0 {
				// Create divisor (10^decimals)
				divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)

				// Convert to big.Float for decimal division with precision
				valueBig := new(big.Float).SetInt(value)
				divisorBig := new(big.Float).SetInt(divisor)
				result := new(big.Float).Quo(valueBig, divisorBig)

				// Format with reasonable precision
				formatted := result.Text('f', 6) // 6 decimal places
				// Remove trailing zeros
				formatted = strings.TrimRight(formatted, "0")
				formatted = strings.TrimRight(formatted, ".")

				return formatted
			} else {
				// No decimals, just show the integer value
				return value.String()
			}
		}
	}

	// Fallback: return as-is
	return amount
}

// calculateTransactionFee calculates and formats the transaction fee from receipt data
func (t *TransactionExplainer) calculateTransactionFee(receipt map[string]interface{}, networkID int64) string {
	// Check if enriched fee data is already available
	if gasFeeUSD, ok := receipt["gas_fee_usd"].(string); ok {
		if gasFeeNative, ok := receipt["gas_fee_native"].(string); ok {
			// Use enriched data if available
			return fmt.Sprintf("$%s (%s ETH)", gasFeeUSD, gasFeeNative)
		}
	}

	// Calculate fee from raw data if enriched data not available
	gasUsedHex, hasGasUsed := receipt["gasUsed"].(string)
	if !hasGasUsed {
		return ""
	}

	gasUsed, err := strconv.ParseUint(gasUsedHex[2:], 16, 64)
	if err != nil {
		return ""
	}

	// Try to get effective gas price, fallback to gas price
	var gasPrice uint64
	if effectiveGasPriceHex, ok := receipt["effectiveGasPrice"].(string); ok {
		if price, err := strconv.ParseUint(effectiveGasPriceHex[2:], 16, 64); err == nil {
			gasPrice = price
		}
	} else if gasPriceHex, ok := receipt["gasPrice"].(string); ok {
		if price, err := strconv.ParseUint(gasPriceHex[2:], 16, 64); err == nil {
			gasPrice = price
		}
	}

	if gasPrice == 0 {
		return ""
	}

	// Calculate fee in wei and convert to ETH
	feeWei := gasUsed * gasPrice
	feeETH := float64(feeWei) / 1e18

	// Get approximate USD value based on network
	nativeTokenPriceUSD := t.getNativeTokenPrice(networkID)
	feeUSD := feeETH * nativeTokenPriceUSD

	// Format as "wei (USD)"
	if feeUSD >= 0.01 {
		return fmt.Sprintf("$%.2f (%.6f ETH)", feeUSD, feeETH)
	} else if feeUSD >= 0.001 {
		return fmt.Sprintf("$%.3f (%.6f ETH)", feeUSD, feeETH)
	} else {
		return fmt.Sprintf("$%.4f (%.6f ETH)", feeUSD, feeETH)
	}
}

// getNativeTokenPrice returns approximate native token price for USD conversion
func (t *TransactionExplainer) getNativeTokenPrice(networkID int64) float64 {
	switch networkID {
	case 1: // Ethereum
		return 2500.0 // ETH price estimate
	case 137: // Polygon
		return 0.85 // MATIC price estimate
	case 42161: // Arbitrum
		return 2500.0 // Uses ETH
	case 10: // Optimism
		return 2500.0 // Uses ETH
	case 56: // BSC
		return 300.0 // BNB price estimate
	case 43114: // Avalanche
		return 25.0 // AVAX price estimate
	case 250: // Fantom
		return 0.25 // FTM price estimate
	default:
		return 2500.0 // Default to ETH price
	}
}

// GetPromptContext provides comprehensive context for the LLM prompt
func (t *TransactionExplainer) GetPromptContext(ctx context.Context, baggage map[string]interface{}) string {
	var contextParts []string

	// Get all context from tools
	if contextProviders, ok := baggage["context_providers"].([]ContextProvider); ok {
		for _, provider := range contextProviders {
			if context := provider.GetPromptContext(ctx, baggage); context != "" {
				contextParts = append(contextParts, context)
			}
		}
	}

	// Only add debug information if DEBUG environment variable is set to "true"
	if os.Getenv("DEBUG") == "true" {
		if t.verbose {
			fmt.Println("=== DEBUG MODE ENABLED - INCLUDING BAGGAGE DEBUG INFO ===")
		}
		
		// Add debug information if available
		if debugInfo, ok := baggage["debug_info"].(map[string]interface{}); ok {
			var debugParts []string
			
			// Add token metadata debug info
			if tokenDebug, ok := debugInfo["token_metadata"].(map[string]interface{}); ok {
				debugParts = append(debugParts, "=== TOKEN METADATA DEBUG ===")
				if discoveredAddresses, ok := tokenDebug["discovered_addresses"].([]string); ok {
					debugParts = append(debugParts, fmt.Sprintf("Discovered %d token addresses: %v", len(discoveredAddresses), discoveredAddresses))
				}
				if rpcResults, ok := tokenDebug["rpc_results"].([]string); ok {
					debugParts = append(debugParts, "RPC Results:")
					for _, result := range rpcResults {
						debugParts = append(debugParts, "  - " + result)
					}
				}
				if rpcErrors, ok := tokenDebug["token_metadata_rpc_errors"].([]string); ok {
					debugParts = append(debugParts, "RPC Errors:")
					for _, err := range rpcErrors {
						debugParts = append(debugParts, "  - " + err)
					}
				}
				if finalMetadata, ok := tokenDebug["final_metadata"].([]string); ok {
					debugParts = append(debugParts, "Final Metadata:")
					for _, metadata := range finalMetadata {
						debugParts = append(debugParts, "  - " + metadata)
					}
				}
			}
			
			// Add transfer enrichment debug info
			if transferDebug, ok := debugInfo["transfer_enrichment"].([]string); ok {
				debugParts = append(debugParts, "=== TRANSFER ENRICHMENT DEBUG ===")
				for _, debug := range transferDebug {
					debugParts = append(debugParts, "  - " + debug)
				}
			}
			
			if len(debugParts) > 0 {
				contextParts = append(contextParts, strings.Join(debugParts, "\n"))
				
				if t.verbose {
					fmt.Printf("=== BAGGAGE DEBUG CONTEXT (%d sections) ===\n", len(debugParts))
					fmt.Println(strings.Join(debugParts, "\n"))
					fmt.Println("=== END OF BAGGAGE DEBUG CONTEXT ===")
					fmt.Println()
				}
			}
		}
	}

	return strings.Join(contextParts, "\n\n")
}
