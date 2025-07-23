package tools

import (
	"context"
	"encoding/json"
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
	return []string{"abi_resolver", "log_decoder", "token_transfer_extractor", "nft_decoder", "token_metadata_enricher", "amounts_finder", "erc20_price_lookup", "monetary_value_enricher", "ens_resolver", "protocol_resolver"}
}

// Process generates explanation using all information from baggage
func (t *TransactionExplainer) Process(ctx context.Context, baggage map[string]interface{}) error {
	// Clean up baggage first - remove unnecessary data before processing
	t.cleanupBaggage(baggage)

	// Add decoded data and raw data to baggage for generateExplanationWithBaggage
	decodedData := &models.DecodedData{}

	// Include events from log decoder
	if events, ok := baggage["events"].([]models.Event); ok {
		decodedData.Events = events
	}

	// Include calls from trace decoder - THIS IS CRITICAL FOR NATIVE ETH TRANSFERS
	if calls, ok := baggage["calls"].([]models.Call); ok {
		decodedData.Calls = calls
	}

	baggage["decoded_data"] = decodedData

	// Collect context from all context providers in the baggage
	var additionalContext []string
	if contextProviders, ok := baggage["context_providers"].([]ContextProvider); ok {
		for _, provider := range contextProviders {
			if context := provider.GetPromptContext(ctx, baggage); context != "" {
				additionalContext = append(additionalContext, context)
			}
		}
		// Remove context providers after use - they're no longer needed
		delete(baggage, "context_providers")
	}

	// Generate explanation with context from other tools
	explanation, err := t.generateExplanationWithBaggage(ctx, baggage, additionalContext)
	if err != nil {
		return fmt.Errorf("failed to generate explanation: %w", err)
	}

	// Add explanation to baggage
	baggage["explanation"] = explanation

	// Final cleanup after processing
	t.finalCleanup(baggage)

	return nil
}

// cleanupBaggage removes unnecessary data to reduce context size
func (t *TransactionExplainer) cleanupBaggage(baggage map[string]interface{}) {
	// Remove debug info unless in DEBUG mode
	if os.Getenv("DEBUG") != "true" {
		delete(baggage, "debug_info")
	}

	// Remove unused keys that take up space
	delete(baggage, "resolved_contracts") // Full ABI data not needed
	delete(baggage, "all_contract_info")  // Comprehensive metadata not needed

	// Clean up raw data - only keep essential transaction info
	if rawData, ok := baggage["raw_data"].(map[string]interface{}); ok {
		cleanedRawData := make(map[string]interface{})

		// Keep only essential transaction info
		if networkID, exists := rawData["network_id"]; exists {
			cleanedRawData["network_id"] = networkID
		}
		if txHash, exists := rawData["tx_hash"]; exists {
			cleanedRawData["tx_hash"] = txHash
		}

		// Keep only essential receipt data
		if receipt, ok := rawData["receipt"].(map[string]interface{}); ok {
			cleanedReceipt := make(map[string]interface{})

			// Only keep fields actually used by transaction explainer
			essentialReceiptFields := []string{"gasUsed", "status", "blockNumber", "from", "to", "gas_fee_usd", "gas_fee_native", "effectiveGasPrice"}
			for _, field := range essentialReceiptFields {
				if value, exists := receipt[field]; exists {
					cleanedReceipt[field] = value
				}
			}

			cleanedRawData["receipt"] = cleanedReceipt
		}

		baggage["raw_data"] = cleanedRawData
	}

	// Clean up events - keep all parameters for LLM analysis
	if events, ok := baggage["events"].([]models.Event); ok {
		for i, event := range events {
			if event.Parameters != nil {
				// Keep ALL parameters - let LLM decide what's meaningful
				// This ensures maximum context for generic transaction analysis
				cleanedParams := make(map[string]interface{})
				for key, value := range event.Parameters {
					cleanedParams[key] = value
				}
				events[i].Parameters = cleanedParams
			}
			// Remove raw topics and data to reduce size (these are internal blockchain fields)
			events[i].Topics = nil
			events[i].Data = ""
		}
		baggage["events"] = events
	}

	// Clean up calls - only keep meaningful ones
	if calls, ok := baggage["calls"].([]models.Call); ok {
		var meaningfulCalls []models.Call
		for _, call := range calls {
			// Only keep calls that are meaningful for explanation
			if call.Contract != "" || call.Method != "" || (call.Value != "" && call.Value != "0" && call.Value != "0x0") {
				// Clean up arguments - remove raw data
				if call.Arguments != nil {
					cleanedArgs := make(map[string]interface{})
					for key, value := range call.Arguments {
						// Only keep essential human-readable arguments
						if key == "contract_name" || key == "contract_symbol" || key == "contract_type" {
							if str, ok := value.(string); ok && str != "" {
								cleanedArgs[key] = str
							}
						}
					}
					call.Arguments = cleanedArgs
				}
				meaningfulCalls = append(meaningfulCalls, call)
			}
		}
		baggage["calls"] = meaningfulCalls
	}

	// Clean up contract addresses - remove duplicates
	if contractAddresses, ok := baggage["contract_addresses"].([]string); ok {
		// Deduplicate and only keep unique addresses
		addressMap := make(map[string]bool)
		var uniqueAddresses []string
		for _, addr := range contractAddresses {
			if !addressMap[strings.ToLower(addr)] {
				addressMap[strings.ToLower(addr)] = true
				uniqueAddresses = append(uniqueAddresses, addr)
			}
		}
		baggage["contract_addresses"] = uniqueAddresses
	}
}

// finalCleanup removes temporary data after explanation is generated
func (t *TransactionExplainer) finalCleanup(baggage map[string]interface{}) {
	// Remove temporary data that's no longer needed
	delete(baggage, "decoded_data")       // Temporary structure for LLM
	delete(baggage, "contract_addresses") // List not needed in final result

	// Keep only essential data for frontend
	// Keep: explanation, transfers, protocols, token_metadata, tags, address_roles, ens_names
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
	explanation, err := t.generateExplanationWithContext(ctx, decodedData, rawData, []string{}, make(map[string]interface{}))
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
	explanation, err := t.generateExplanationWithContext(ctx, decodedData, rawData, additionalContext, baggage)
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
func (t *TransactionExplainer) generateExplanationWithContext(ctx context.Context, decodedData *models.DecodedData, rawData map[string]interface{}, additionalContext []string, baggage map[string]interface{}) (*models.ExplanationResult, error) {
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

	explanation := t.parseExplanationResponse(ctx, responseText, decodedData, rawData, baggage)

	return explanation, nil
}

// buildExplanationPrompt creates the prompt for the LLM
func (t *TransactionExplainer) buildExplanationPrompt(decodedData *models.DecodedData, rawData map[string]interface{}, additionalContexts []string) string {
	prompt := `You are a blockchain transaction analyzer. Provide a VERY SHORT, precise summary of what this transaction accomplished. Keep it under 30 words - users have only 2 seconds to read.

Focus ONLY on the main action. Don't explain blockchain basics or add warnings.

## Decoded Transaction Data:

### Function Calls:`

	// Add calls information - CLEANED UP VERSION
	for i, call := range decodedData.Calls {
		// Only include calls that are meaningful for explanation
		if call.Contract == "" && call.Method == "" && call.Value == "" {
			continue // Skip empty/meaningless calls
		}

		prompt += fmt.Sprintf(`

Call #%d:`,
			i+1)

		if call.Contract != "" {
			prompt += fmt.Sprintf(`
- Contract: %s`, call.Contract)
		}

		if call.Method != "" {
			prompt += fmt.Sprintf(`
- Method: %s`, call.Method)
		}

		if call.CallType != "" {
			prompt += fmt.Sprintf(`
- Type: %s`, call.CallType)
		}

		// Only show ETH value if significant (> 0)
		if call.Value != "" && call.Value != "0" && call.Value != "0x" && call.Value != "0x0" {
			ethValue := t.weiToEther(call.Value)
			if ethValue != "0" {
				prompt += fmt.Sprintf(`
- ETH Value: %s`, ethValue)
			}
		}

		if !call.Success {
			prompt += fmt.Sprintf(`
- Failed: %s`, call.ErrorReason)
		}

		// Only include essential arguments, skip raw hex data
		if len(call.Arguments) > 0 {
			essentialArgs := make(map[string]interface{})
			for key, value := range call.Arguments {
				// Only include human-readable arguments, skip raw data
				if key == "contract_name" || key == "contract_symbol" || key == "contract_type" {
					if str, ok := value.(string); ok && str != "" {
						essentialArgs[key] = str
					}
				}
			}

			if len(essentialArgs) > 0 {
				prompt += `
- Info:`
				for key, value := range essentialArgs {
					prompt += fmt.Sprintf(`
  - %s: %v`, key, value)
				}
			}
		}
	}

	prompt += `

### Events Emitted:`

	// Add events information - INCLUDE ALL DECODED FORMATS
	for i, event := range decodedData.Events {
		prompt += fmt.Sprintf(`

Event #%d:
- Contract: %s
- Event: %s`,
			i+1, event.Contract, event.Name)

		// Include ALL meaningful parameters with their decoded formats
		if len(event.Parameters) > 0 {
			prompt += "\n- Parameters:"

			// Group parameters by base name (param_1, param_2, etc.)
			paramGroups := make(map[string]map[string]interface{})

			for key, value := range event.Parameters {
				// Include ALL parameters - let LLM decide what's meaningful for final explanation

				// Extract base parameter name (e.g., "param_1" from "param_1_decimal")
				var baseName string
				if strings.Contains(key, "_") {
					parts := strings.Split(key, "_")
					if len(parts) >= 2 && (parts[0] == "param" || parts[0] == "topic") {
						baseName = parts[0] + "_" + parts[1]
					} else {
						baseName = key
					}
				} else {
					baseName = key
				}

				if paramGroups[baseName] == nil {
					paramGroups[baseName] = make(map[string]interface{})
				}
				paramGroups[baseName][key] = value
			}

			// Display parameters with all their decoded formats
			for baseName, group := range paramGroups {
				if baseValue, exists := group[baseName]; exists {
					prompt += fmt.Sprintf("\n  - %s: %v", baseName, baseValue)

					// Add decoded formats on the same line for context
					var decodedInfo []string

					if decimal, exists := group[baseName+"_decimal"]; exists {
						decodedInfo = append(decodedInfo, fmt.Sprintf("decimal: %v", decimal))
					}

					if address, exists := group[baseName+"_address"]; exists {
						decodedInfo = append(decodedInfo, fmt.Sprintf("address: %v", address))
					}

					if boolean, exists := group[baseName+"_boolean"]; exists {
						decodedInfo = append(decodedInfo, fmt.Sprintf("boolean: %v", boolean))
					}

					if utf8, exists := group[baseName+"_utf8"]; exists {
						decodedInfo = append(decodedInfo, fmt.Sprintf("utf8: \"%v\"", utf8))
					}

					if paramType, exists := group[baseName+"_type"]; exists {
						decodedInfo = append(decodedInfo, fmt.Sprintf("type: %v", paramType))
					}

					if len(decodedInfo) > 0 {
						prompt += fmt.Sprintf(" (%s)", strings.Join(decodedInfo, ", "))
					}
				}
			}
		}
	}

	// Token transfers will be included via Additional Context from TokenTransferExtractor

	// Add raw transaction context if available
	if rawData != nil {
		prompt += `

### Transaction Context:`

		if receipt, ok := rawData["receipt"].(map[string]interface{}); ok {
			// Always include transaction sender prominently
			if from, ok := receipt["from"].(string); ok {
				prompt += fmt.Sprintf(`
- TRANSACTION SENDER: %s (the address that initiated this transaction)`, from)
			}
			if to, ok := receipt["to"].(string); ok {
				prompt += fmt.Sprintf(`
- Contract Called: %s`, to)
			}
			if gasUsed, ok := receipt["gasUsed"].(string); ok {
				prompt += fmt.Sprintf(`
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

🔥 MANDATORY ENS REQUIREMENT: If ANY address in the transaction has an ENS name (shown in "ENS Names Resolved" or "Address Formatting Guide" sections), you MUST include it in the format "0x1234...5678 (ens-name.eth)" in your explanation. This is non-negotiable. 

CRITICAL SPECIFICITY REQUIREMENTS:
- NEVER use vague terms like "multiple", "several", "various", "some", "many" 
- ALWAYS use EXACT numbers: "unlocked 8 orders", "transferred 5 tokens", "swapped 2,485.75 USDT"
- If you see specific quantities in the transaction data, USE THEM EXPLICITLY
- Examples: ❌"multiple tokens" → ✅"8 tokens", ❌"several NFTs" → ✅"3 NFTs", ❌"various transfers" → ✅"5 transfers"
- Count events, calls, transfers, and show the exact numbers
- Be precise about amounts, quantities, and counts in ALL cases

CRITICAL FORMATTING RULE - AVOID REDUNDANCY:
- NEVER repeat token amounts or token names in the same sentence
- Format: "[sender] performed [action] involving [amount] [token] via [protocol] + $[gas] gas" ✅
- If you mention a token amount and name once, don't repeat them again in the same sentence
- Keep the format clean and concise - one mention per token is enough

TRANSACTION ANALYSIS APPROACH:
- Analyze the raw transaction data (events, calls, transfers) without preconceived categories
- Let the transaction data tell you what happened - don't assume specific action types
- Focus on the actual flow of value: who sent what to whom, and what they received in return
- Use event parameters and function calls to understand the transaction's purpose
- Describe what actually occurred based on the data, not predetermined transaction types

ZERO ADDRESS PATTERN RECOGNITION:
- When tokens/NFTs are transferred FROM the zero address (0x0000000000000000000000000000000000000000), this typically indicates new token creation
- When tokens/NFTs are transferred TO the zero address, this typically indicates token destruction
- Use descriptive language based on the actual data rather than hardcoded terms

VALUE FLOW ANALYSIS:
- For transactions with multiple token transfers, trace the complete flow
- Identify who initiated the transaction (transaction sender)
- Identify what value entered and exited the system
- Focus on the NET EFFECT for the actual user
- Look for intermediary steps but describe the overall outcome
- Don't assume specific patterns - let the data guide the description

USER IDENTIFICATION IN DEFI:
- CRITICAL: Always check for "ACTUAL USER" in Payment Flow Analysis section
- When ACTUAL USER is provided, use that address, NOT the contract/router addresses
- DeFi transactions often use intermediary contracts - show the real beneficiary
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

EVENT PARAMETER USAGE:
- CRITICAL: Use event parameters to provide SPECIFIC DETAILS in your explanation
- When events have meaningful parameter names (user, role, enabled, roleId, etc.), ALWAYS include them in the text
- For role/permission events: Include role ID/name and whether it was enabled/disabled/granted/revoked
- For user management: Include both the admin who made the change AND the affected user
- For numeric parameters: Include specific numbers (role ID 7, token ID 1234, etc.)
- For boolean parameters: Include the state (enabled/disabled, approved/revoked, active/inactive)
- Always prioritize the transaction sender when describing WHO initiated the action
- NEVER use vague terms like "updated a user role" when specific details are available

CRITICAL ENS NAME REQUIREMENTS:
- MANDATORY: Always include ENS names in the final explanation text when available
- Use the format: "0x1234...5678 (ens-name.eth)" for addresses with ENS names
- Check the "ENS Names Resolved" and "Address Formatting Guide" sections for available ENS names
- NEVER use just the shortened address if an ENS name exists - always include both
- This applies to ALL addresses in the explanation: transaction sender, event parameters, contract addresses

PROTOCOL USAGE:
- Always include specific protocol/aggregator names when available from the "Protocol Detection" section
- Use specific protocol names provided in the context instead of generic terms 
- For aggregators, mention the aggregator name from detected protocols
- For DEX protocols, include the protocol name from detected protocols

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

GENERIC EXAMPLES (describe what actually happened based on data):
- "[sender] ([ens]) performed action involving [amount] [token] with [recipient] ([ens]) + $[gas] gas"
- "[sender] ([ens]) interacted with [protocol] using [amount] [token] + $[gas] gas"
- "[sender] ([ens]) executed operation resulting in [outcome] + $[gas] gas"
- "Transaction executed by [sender] ([ens]) involving [details] + $[gas] gas"
- "[sender] ([ens]) completed operation on [protocol] with [specifications] + $[gas] gas"

CRITICAL: In ALL examples above, notice how ENS names are ALWAYS included when addresses appear. This is MANDATORY - never omit ENS names from the final explanation.

Be specific about amounts, tokens, protocols, and main action. No explanations or warnings.`

	return prompt
}

// parseExplanationResponse parses the LLM response and creates the result structure
func (t *TransactionExplainer) parseExplanationResponse(ctx context.Context, response string, decodedData *models.DecodedData, rawData map[string]interface{}, baggage map[string]interface{}) *models.ExplanationResult {
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

			// Gas fee information is provided by MonetaryValueEnricher in context - no duplication needed
		}
	}

	// Token transfers should be provided via the Process method/baggage
	// For Run method, leave empty for now
	result.Transfers = []models.TokenTransfer{}

	// Get tags from tag resolver (probabilistic approach)
	if tags, ok := baggage["tags"].([]string); ok {
		result.Tags = tags
	} else {
		result.Tags = []string{} // Empty if tag resolver didn't run
	}

	// Generate AI-enhanced links with meaningful labels
	result.Links = t.generateIntelligentLinks(ctx, result.TxHash, result.NetworkID, baggage)

	// Add address categories to metadata for frontend legend grouping
	if addressRoles, ok := baggage["address_roles"].(map[string]map[string]string); ok && len(addressRoles) > 0 {
		// Create categories map for frontend
		categories := make(map[string][]map[string]string)

		// Group addresses by category
		for address, roleData := range addressRoles {
			if roleData != nil {
				role := roleData["role"]
				category := roleData["category"]

				if role != "" && category != "" {
					// Initialize category array if not exists
					if categories[category] == nil {
						categories[category] = make([]map[string]string, 0)
					}

					// Add address to category
					categories[category] = append(categories[category], map[string]string{
						"address": address,
						"role":    role,
					})
				}
			}
		}

		// Add to metadata for frontend access
		result.Metadata["address_categories"] = categories
		result.Metadata["available_categories"] = []string{}

		// Track which categories are actually used
		for category := range categories {
			result.Metadata["available_categories"] = append(result.Metadata["available_categories"].([]string), category)
		}
	}

	return result
}

// generateIntelligentLinks creates explorer links with AI-inferred meaningful labels
func (t *TransactionExplainer) generateIntelligentLinks(ctx context.Context, txHash string, networkID int64, baggage map[string]interface{}) map[string]string {
	links := make(map[string]string)

	if txHash == "" || networkID <= 0 {
		return links
	}

	network, exists := models.GetNetwork(networkID)
	if !exists {
		return links
	}

	// Always add the main transaction link first
	links["Main Transaction"] = fmt.Sprintf("%s/tx/%s", network.Explorer, txHash)

	// Get all relevant addresses and contracts from the transaction context
	addressRoles, err := t.inferAddressRoles(ctx, baggage, networkID)
	if err != nil {
		// Fallback to simple contract links if AI inference fails
		return t.generateFallbackLinks(txHash, networkID, baggage)
	}

	// Store address roles in baggage for reuse in parseExplanationResponse
	baggage["address_roles"] = addressRoles

	// Create links with meaningful role-based labels using ALL addresses from role inference
	// This ensures router addresses and other important contracts are included even if
	// they're not in the contract_addresses baggage due to pipeline timing issues
	for address, roleData := range addressRoles {
		if address != "" && roleData != nil {
			role := roleData["role"]
			category := roleData["category"]
			if role != "" && category != "" {
				links[role] = fmt.Sprintf("%s/address/%s", network.Explorer, address)
			}
		}
	}

	return links
}

// inferAddressRoles uses AI to infer meaningful roles for addresses and contracts
func (t *TransactionExplainer) inferAddressRoles(ctx context.Context, baggage map[string]interface{}, networkID int64) (map[string]map[string]string, error) {
	// Build context for AI analysis
	prompt := t.buildAddressRolePrompt(baggage, networkID)

	if t.verbose {
		fmt.Println("=== ADDRESS ROLE INFERENCE: PROMPT ===")
		fmt.Println(prompt)
		fmt.Println("=== END PROMPT ===")
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
		fmt.Println("=== ADDRESS ROLE INFERENCE: LLM RESPONSE ===")
		fmt.Println(responseText)
		fmt.Println("=== END RESPONSE ===")
		fmt.Println()
	}

	// Parse the response
	return t.parseAddressRoleResponse(responseText)
}

// buildAddressRolePrompt creates the prompt for AI address role inference
func (t *TransactionExplainer) buildAddressRolePrompt(baggage map[string]interface{}, networkID int64) string {
	prompt := `You are a blockchain transaction analyst. Analyze this transaction and identify the role of each address/contract involved, AND categorize them into groups. Provide meaningful labels that help users understand what each address represents in the context of this specific transaction.

TRANSACTION CONTEXT:`

	// Add token metadata context FIRST - this is critical for distinguishing tokens from protocols
	if tokenMetadata, ok := baggage["token_metadata"].(map[string]*TokenMetadata); ok && len(tokenMetadata) > 0 {
		prompt += "\n\nTOKEN CONTRACTS (these addresses are ERC20/ERC721/ERC1155 tokens, NOT protocol contracts):"
		for addr, metadata := range tokenMetadata {
			prompt += fmt.Sprintf("\n- %s: %s (%s) [%s token]", addr, metadata.Name, metadata.Symbol, metadata.Type)
		}
	}

	// Add protocol context
	if protocols, ok := baggage["protocols"].([]ProbabilisticProtocol); ok && len(protocols) > 0 {
		prompt += "\n\nDETECTED PROTOCOLS:"
		for _, protocol := range protocols {
			prompt += fmt.Sprintf("\n- %s (%s %s)", protocol.Name, protocol.Type, protocol.Version)
		}
	}

	// Add transfers context
	if transfers, ok := baggage["transfers"].([]models.TokenTransfer); ok && len(transfers) > 0 {
		prompt += "\n\nTOKEN TRANSFERS:"
		for i, transfer := range transfers {
			prompt += fmt.Sprintf("\n- Transfer #%d: %s → %s", i+1, transfer.From, transfer.To)
			if transfer.Symbol != "" && transfer.FormattedAmount != "" {
				prompt += fmt.Sprintf(" (%s %s)", transfer.FormattedAmount, transfer.Symbol)
			}
			prompt += fmt.Sprintf(" [Contract: %s]", transfer.Contract)
		}
	}

	// Add contract addresses context
	if contractAddresses, ok := baggage["contract_addresses"].([]string); ok && len(contractAddresses) > 0 {
		prompt += "\n\nCONTRACT ADDRESSES:"
		for _, addr := range contractAddresses {
			prompt += fmt.Sprintf("\n- %s", addr)
		}
	}

	// Add events context with spender extraction
	if events, ok := baggage["events"].([]models.Event); ok && len(events) > 0 {
		prompt += "\n\nEVENTS:"
		for _, event := range events {
			eventInfo := fmt.Sprintf("%s event on %s", event.Name, event.Contract)

			// Include ALL event parameters generically - no special event handling
			if event.Parameters != nil {
				var paramStrings []string
				for paramName, paramValue := range event.Parameters {
					paramStrings = append(paramStrings, fmt.Sprintf("%s: %v", paramName, paramValue))
				}
				if len(paramStrings) > 0 {
					eventInfo += fmt.Sprintf(" (%s)", strings.Join(paramStrings, ", "))
				}
			}

			prompt += fmt.Sprintf("\n- %s", eventInfo)
		}
	}

	// Add raw transaction context
	if rawData, ok := baggage["raw_data"].(map[string]interface{}); ok {
		if receipt, ok := rawData["receipt"].(map[string]interface{}); ok {
			if from, ok := receipt["from"].(string); ok {
				prompt += fmt.Sprintf("\n\nTRANSACTION FROM: %s", from)
			}
			if to, ok := receipt["to"].(string); ok {
				prompt += fmt.Sprintf("\nTRANSACTION TO: %s", to)
			}
		}
	}

	// Add network context
	if network, exists := models.GetNetwork(networkID); exists {
		prompt += fmt.Sprintf("\n\nNETWORK: %s", network.Name)
	}

	prompt += `

CRITICAL RULE - TOKEN CONTRACTS vs PROTOCOL CONTRACTS:
- If an address appears in the "TOKEN CONTRACTS" section above, it MUST be labeled as "Token Contract ([SYMBOL])" with category "token"
- NEVER identify a token contract address as a protocol router, aggregator, or other protocol contract
- Protocol contracts are routers, pools, aggregators - NOT the tokens themselves
- Use spender addresses from Approval events as potential protocol contracts, NOT the token contract

ROLE IDENTIFICATION AND CATEGORIZATION:
Based on the transaction context, identify the role AND category for each address:

CATEGORY GUIDELINES (be creative and context-appropriate):
- Use intuitive, descriptive categories that make sense for this specific transaction
- Common categories include: "user", "trader", "protocol", "token", "nft", "defi", "exchange", "bridge", etc.
- But feel free to create more specific categories like "lending", "staking", "gaming", "dao", "marketplace" if they better describe the context
- Group similar addresses together with consistent category names
- Prioritize clarity and user understanding over strict adherence to predefined lists

ROLE EXAMPLES BY COMMON CATEGORIES:

USER-TYPE CATEGORIES:
- "Token Holder" - address holding/managing tokens
- "Transaction Initiator" - address that started the transaction
- "Recipient" - address receiving tokens/NFTs
- "Investor" - address making investment decisions

TRADER-TYPE CATEGORIES:
- "Token Trader" - address performing token swaps
- "NFT Trader" - address trading NFTs
- "Arbitrageur" - address performing arbitrage
- "Liquidity Provider" - address providing/managing liquidity

PROTOCOL-TYPE CATEGORIES:
- "DEX Router" - router contracts for decentralized exchanges
- "Lending Pool" - lending protocol contracts
- "Liquidity Pool" - AMM pool contracts
- "Aggregator" - DEX aggregator contracts
- "NFT Marketplace" - NFT trading platforms
- "Bridge" - cross-chain bridge contracts

TOKEN-TYPE CATEGORIES:
- "Token Contract" - ERC20/ERC721/ERC1155 contracts
- "Governance Token" - tokens used for DAO governance
- "Utility Token" - tokens with specific utility functions

SPECIALIZED CATEGORIES (use when contextually appropriate):
- "defi" - for DeFi protocol addresses
- "gaming" - for gaming-related contracts
- "dao" - for DAO governance addresses  
- "staking" - for staking-related contracts
- "bridge" - for cross-chain bridge contracts
- "oracle" - for price feed and oracle contracts

PRIORITIZATION:
1. Focus on the PRIMARY transaction purpose (swap, lend, NFT purchase, etc.)
2. Identify the MAIN USER (the address initiating the transaction) 
3. Identify TOKEN CONTRACTS first using the "TOKEN CONTRACTS" section
4. Identify PROTOCOL CONTRACTS (routers, pools, marketplaces) separately
5. Include significant addresses only (limit to 6-8 most relevant)

OUTPUT FORMAT:
Respond with a JSON object mapping addresses to their role and category:
{
  "0x1234...5678": {
    "role": "Token Trader",
    "category": "trader"
  },
  "0xabcd...ef01": {
    "role": "DEX Router", 
    "category": "protocol"
  },
  "0x9876...4321": {
    "role": "Token Contract (USDT)",
    "category": "token"
  }
}

CORRECT EXAMPLE - Token Approval Transaction:
{
  "[user_address_from_transaction]": {
    "role": "Token Holder",
    "category": "user"
  },
  "[token_contract_from_token_metadata]": {
    "role": "Token Contract ([token_symbol_from_metadata])",
    "category": "token"
  },
  "[spender_address_from_approval_event]": {
    "role": "DEX Router",
    "category": "protocol"
  }
}

Analyze the transaction context and identify the most meaningful roles and categories for up to 6-8 key addresses:
`

	return prompt
}

// parseAddressRoleResponse parses the LLM response into address-role mappings with categories
func (t *TransactionExplainer) parseAddressRoleResponse(response string) (map[string]map[string]string, error) {
	response = strings.TrimSpace(response)

	// Look for JSON object
	jsonStart := strings.Index(response, "{")
	jsonEnd := strings.LastIndex(response, "}")

	if jsonStart == -1 || jsonEnd == -1 || jsonEnd <= jsonStart {
		return nil, fmt.Errorf("no valid JSON object found in response")
	}

	jsonStr := response[jsonStart : jsonEnd+1]

	// Parse JSON with the new format: address -> {role, category}
	var addressRoles map[string]map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &addressRoles); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	// Convert to string-string map and validate
	cleaned := make(map[string]map[string]string)
	for address, roleData := range addressRoles {
		address = strings.TrimSpace(address)
		if address == "" {
			continue
		}

		role := ""
		category := ""

		// Extract role and category from the nested object
		if roleStr, ok := roleData["role"].(string); ok {
			role = strings.TrimSpace(roleStr)
		}
		if categoryStr, ok := roleData["category"].(string); ok {
			category = strings.TrimSpace(categoryStr)
		}

		// Ensure both role and category are present
		if role != "" && category != "" {
			cleaned[address] = map[string]string{
				"role":     role,
				"category": category,
			}
		}
	}

	return cleaned, nil
}

// generateFallbackLinks creates basic contract links when AI inference fails
func (t *TransactionExplainer) generateFallbackLinks(txHash string, networkID int64, baggage map[string]interface{}) map[string]string {
	links := make(map[string]string)

	if network, exists := models.GetNetwork(networkID); exists {
		links["Main Transaction"] = fmt.Sprintf("%s/tx/%s", network.Explorer, txHash)

		// Add basic contract links
		if contractAddresses, ok := baggage["contract_addresses"].([]string); ok {
			for i, address := range contractAddresses {
				if i < 5 { // Limit to avoid too many links
					label := fmt.Sprintf("Contract %d", i+1)
					links[label] = fmt.Sprintf("%s/address/%s", network.Explorer, address)
				}
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
						debugParts = append(debugParts, "  - "+result)
					}
				}
				if rpcErrors, ok := tokenDebug["token_metadata_rpc_errors"].([]string); ok {
					debugParts = append(debugParts, "RPC Errors:")
					for _, err := range rpcErrors {
						debugParts = append(debugParts, "  - "+err)
					}
				}
				if finalMetadata, ok := tokenDebug["final_metadata"].([]string); ok {
					debugParts = append(debugParts, "Final Metadata:")
					for _, metadata := range finalMetadata {
						debugParts = append(debugParts, "  - "+metadata)
					}
				}
			}

			// Add transfer enrichment debug info
			if transferDebug, ok := debugInfo["transfer_enrichment"].([]string); ok {
				debugParts = append(debugParts, "=== TRANSFER ENRICHMENT DEBUG ===")
				for _, debug := range transferDebug {
					debugParts = append(debugParts, "  - "+debug)
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
