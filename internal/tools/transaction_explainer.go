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
	llm             llms.Model
	contextProviders []ContextProvider
}

// NewTransactionExplainer creates a new TransactionExplainer tool
func NewTransactionExplainer(llm llms.Model, contextProviders ...ContextProvider) *TransactionExplainer {
	return &TransactionExplainer{
		llm:              llm,
		contextProviders: contextProviders,
	}
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
	explanation, err := t.generateExplanation(ctx, decodedData, rawData)
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

// generateExplanation uses the LLM to create a human-readable explanation
func (t *TransactionExplainer) generateExplanation(ctx context.Context, decodedData *models.DecodedData, rawData map[string]interface{}) (*models.ExplanationResult, error) {
	// Collect additional context from all context providers
	var additionalContexts []string
	for _, provider := range t.contextProviders {
		contextData := map[string]interface{}{
			"decoded_data": decodedData,
			"raw_data":     rawData,
		}
		if context := provider.GetPromptContext(ctx, contextData); context != "" {
			additionalContexts = append(additionalContexts, context)
		}
	}

	// Build the prompt with additional context
	prompt := t.buildExplanationPrompt(decodedData, rawData, additionalContexts)

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

	// Add formatted token transfers if available
	transfers := t.extractTokenTransfers(decodedData.Events)
	if len(transfers) > 0 {
		prompt += `

### Formatted Token Transfers:`
		for i, transfer := range transfers {
			prompt += fmt.Sprintf(`

Transfer #%d:
- Type: %s
- Contract: %s`, i+1, transfer.Type, transfer.Contract)
			
			if transfer.Name != "" && transfer.Symbol != "" {
				prompt += fmt.Sprintf(`
- Token: %s (%s)`, transfer.Name, transfer.Symbol)
			} else if transfer.Symbol != "" {
				prompt += fmt.Sprintf(`
- Symbol: %s`, transfer.Symbol)
			}
			

			
			prompt += fmt.Sprintf(`
- From: %s
- To: %s`, transfer.From, transfer.To)
			
			if transfer.Amount != "" {
				// Format the amount for display
				if transfer.Decimals > 0 {
					prompt += fmt.Sprintf(`
- Amount: %s (formatted from raw hex)`, t.formatAmount(transfer.Amount, transfer.Decimals))
				} else {
					prompt += fmt.Sprintf(`
- Amount: %s`, transfer.Amount)
				}
			}
			
			if transfer.TokenID != "" {
				prompt += fmt.Sprintf(`
- Token ID: %s`, transfer.TokenID)
			}
		}
	}

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

IMPORTANT: 
- Use the amounts from "Formatted Token Transfers" section (not raw hex values from events).
- When mentioning ERC20 tokens, include their current USD price in parentheses if provided in Additional Context (e.g., "USDT ($1.00)").
- Only include prices for tokens that are listed in the Additional Context section above.

Examples:
- "Transferred 100 USDC ($1.00) from Alice to Bob"
- "Swapped 1 ETH for 2,500 USDT ($1.00) on Uniswap"  
- "Approved Uniswap to spend unlimited DAI ($0.99)"
- "Minted 5 NFTs from BoredApes collection"

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

	// Extract token transfers from events
	result.Transfers = t.extractTokenTransfers(decodedData.Events)

	// Generate wallet effects from transfers
	result.Effects = t.generateWalletEffects(result.Transfers)

	// Generate tags based on transaction content
	result.Tags = t.generateTags(decodedData)

	// Generate links to explorers
	result.Links = t.generateLinks(result.TxHash, result.NetworkID, decodedData)

	return result
}

// extractTokenTransfers extracts token transfers from events
func (t *TransactionExplainer) extractTokenTransfers(events []models.Event) []models.TokenTransfer {
	var transfers []models.TokenTransfer

	for _, event := range events {
		if event.Name == "Transfer" {
			transfer := models.TokenTransfer{
				Contract: event.Contract,
			}

			// Extract from, to, and amount/tokenId from parameters
			if params := event.Parameters; params != nil {
				if from, ok := params["from"].(string); ok {
					transfer.From = from
				}
				if to, ok := params["to"].(string); ok {
					transfer.To = to
				}
				if value, ok := params["value"].(string); ok {
					transfer.Amount = value
					transfer.Type = "ERC20" // Assume ERC20 for now
				}
				if tokenId, ok := params["tokenId"].(string); ok {
					transfer.TokenID = tokenId
					transfer.Type = "ERC721" // NFT transfer
				}
			}

			transfers = append(transfers, transfer)
		}
	}

	return transfers
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

 