package tools

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/tmc/langchaingo/llms"
	"github.com/txplain/txplain/internal/models"
)

// TransactionExplainer generates human-readable explanations from decoded transaction data
type TransactionExplainer struct {
	llm llms.Model
}

// NewTransactionExplainer creates a new TransactionExplainer tool
func NewTransactionExplainer(llm llms.Model) *TransactionExplainer {
	return &TransactionExplainer{
		llm: llm,
	}
}

// Dependencies returns the tools this processor depends on
func (t *TransactionExplainer) Dependencies() []string {
	return []string{"abi_resolver", "log_decoder", "token_transfer_extractor", "token_metadata_enricher", "erc20_price_lookup", "monetary_value_enricher", "ens_resolver", "protocol_resolver"}
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
	// Build the prompt with additional context from baggage
	prompt := t.buildExplanationPromptFromBaggage(baggage, additionalContext)

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

	explanation := t.parseExplanationResponseFromBaggage(responseText, baggage)

	return explanation, nil
}

// generateExplanationWithContext uses the LLM to create a human-readable explanation with additional context
func (t *TransactionExplainer) generateExplanationWithContext(ctx context.Context, decodedData *models.DecodedData, rawData map[string]interface{}, additionalContext []string) (*models.ExplanationResult, error) {
	// Build the prompt with additional context
	prompt := t.buildExplanationPrompt(decodedData, rawData, additionalContext)

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

	explanation := t.parseExplanationResponse(responseText, decodedData, rawData)

	return explanation, nil
}

// buildExplanationPromptFromBaggage creates the prompt for the LLM using data from baggage
func (t *TransactionExplainer) buildExplanationPromptFromBaggage(baggage map[string]interface{}, additionalContexts []string) string {
	// Get decoded data and raw data from baggage
	decodedData, _ := baggage["decoded_data"].(*models.DecodedData)
	rawData, _ := baggage["raw_data"].(map[string]interface{})

	// If no decoded data, create empty structure
	if decodedData == nil {
		decodedData = &models.DecodedData{}
	}

	return t.buildExplanationPrompt(decodedData, rawData, additionalContexts)
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

MULTI-HOP SWAP DETECTION:
- For complex transactions with multiple token transfers, focus on the NET EFFECT for the user
- Look for the pattern: User sends Token A → Multiple intermediary transfers → User receives Token B
- The user is typically the address that appears in both the first "from" and final "to" positions
- Ignore intermediary router/contract addresses that facilitate the swap
- The final output token is what the user ultimately receives, not intermediate tokens like WETH

IMPORTANT: 
- Use enriched monetary values from "Enriched Token Transfers" section when available (FormattedAmount and USD Value fields).
- Prefer specific USD values from the enriched data over raw hex values or basic token prices.
- If enriched transfers show "Amount: 43.94 ATH" and "USD Value: $1.45", use those exact values.
- Only fall back to raw data or basic prices if enriched values are not available.
- Always use the total converted amount, not the base unit price.
- Use the address formatting provided in the "Address Formatting Guide" section.
- Follow the address usage instructions from the ENS Names section.
- If gas fees in USD are provided in the "Transaction Fees" section, include them in your explanation when relevant (e.g., "with $2.15 gas fee" or "paying $0.85 in fees").

Examples:
- "Transferred 43.94 ATH ($1.45 USD) from one wallet to another"
- "Swapped 1 ETH for 2,485.75 USDT ($2,485.75 USD) on Uniswap"  
- "Swapped 100 USDT ($100) for 57,071 GrowAI tokens via DEX aggregator"
- "Approved Uniswap to spend unlimited DAI"
- "Minted 5 NFTs from BoredApes collection"
- "Transferred 100 USDC from 0x1234...5678 (alice.eth) to 0x9876...4321 (bob.eth)"
- "Swapped 0.5 ETH for 1,250 USDC ($1,250) with $2.15 gas fee"
- "Transferred 1,000 USDT ($1,000) paying $0.85 in fees"

Be specific about amounts, tokens, and main action. No explanations or warnings.`

	return prompt
}

// parseExplanationResponse parses the LLM response and creates the result structure
func (t *TransactionExplainer) parseExplanationResponse(response string, decodedData *models.DecodedData, rawData map[string]interface{}) *models.ExplanationResult {
	result := &models.ExplanationResult{
		Summary:   response, // For now, use the full response as summary
		Effects:   []models.WalletEffect{},
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
		}
	}

	// Token transfers should be provided via the Process method/baggage
	// For Run method, leave empty for now
	result.Transfers = []models.TokenTransfer{}

	// Generate wallet effects from transfers
	result.Effects = t.generateWalletEffects(result.Transfers)

	// Generate tags based on transaction content
	result.Tags = t.generateTags(decodedData)

	// Generate links to explorers
	result.Links = t.generateLinks(result.TxHash, result.NetworkID, decodedData)

	return result
}

// parseExplanationResponseFromBaggage parses the LLM response using data from baggage
func (t *TransactionExplainer) parseExplanationResponseFromBaggage(response string, baggage map[string]interface{}) *models.ExplanationResult {
	// Get data from baggage
	decodedData, _ := baggage["decoded_data"].(*models.DecodedData)
	rawData, _ := baggage["raw_data"].(map[string]interface{})

	// If no decoded data, create empty structure
	if decodedData == nil {
		decodedData = &models.DecodedData{}
	}

	result := &models.ExplanationResult{
		Summary:   response, // For now, use the full response as summary
		Effects:   []models.WalletEffect{},
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
		}
	}

	// Get transfers from baggage (populated by TokenTransferExtractor)
	if transfers, ok := baggage["transfers"].([]models.TokenTransfer); ok {
		result.Transfers = transfers
	}

	// Generate wallet effects from transfers
	result.Effects = t.generateWalletEffects(result.Transfers)

	// Generate tags based on transaction content
	result.Tags = t.generateTags(decodedData)

	// Generate links to explorers
	result.Links = t.generateLinks(result.TxHash, result.NetworkID, decodedData)

	return result
}

// generateWalletEffects creates wallet effects from transfers
func (t *TransactionExplainer) generateWalletEffects(transfers []models.TokenTransfer) []models.WalletEffect {
	effectsMap := make(map[string]*models.WalletEffect)

	for _, transfer := range transfers {
		// Effect on sender
		if transfer.From != "" {
			if effect, exists := effectsMap[transfer.From]; exists {
				effect.Transfers = append(effect.Transfers, transfer)
			} else {
				effectsMap[transfer.From] = &models.WalletEffect{
					Address:   transfer.From,
					Transfers: []models.TokenTransfer{transfer},
				}
			}
		}

		// Effect on receiver
		if transfer.To != "" {
			if effect, exists := effectsMap[transfer.To]; exists {
				effect.Transfers = append(effect.Transfers, transfer)
			} else {
				effectsMap[transfer.To] = &models.WalletEffect{
					Address:   transfer.To,
					Transfers: []models.TokenTransfer{transfer},
				}
			}
		}
	}

	// Convert map to slice
	var effects []models.WalletEffect
	for _, effect := range effectsMap {
		effects = append(effects, *effect)
	}

	return effects
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

				// Convert to big.Float for decimal division
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
