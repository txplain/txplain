package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tmc/langchaingo/llms"
	"github.com/txplain/txplain/internal/models"
)

// TransactionExplainer generates human-readable explanations of blockchain transactions using TRUE RAG
type TransactionExplainer struct {
	llm        llms.Model
	ragService *RAGSearchService
	verbose    bool
}

// NewTransactionExplainer creates a new transaction explainer with TRUE RAG capabilities
func NewTransactionExplainer(llm llms.Model, staticProvider *StaticContextProvider) *TransactionExplainer {
	ragService := NewRAGSearchService(staticProvider)
	return &TransactionExplainer{
		llm:        llm,
		ragService: ragService,
		verbose:    false,
	}
}

// SetVerbose enables or disables verbose logging
func (t *TransactionExplainer) SetVerbose(verbose bool) {
	t.verbose = verbose
	if t.ragService != nil {
		t.ragService.SetVerbose(verbose)
	}
}

// Dependencies returns the tools this processor depends on
func (t *TransactionExplainer) Dependencies() []string {
	return []string{
		"abi_resolver", "log_decoder", "trace_decoder", "ens_resolver",
		"token_metadata_enricher", "erc20_price_lookup", "monetary_value_enricher",
		"protocol_resolver", "tag_resolver", "static_context_provider",
	}
}

// Process generates explanations using TRUE RAG with autonomous function calling
func (t *TransactionExplainer) Process(ctx context.Context, baggage map[string]interface{}) error {
	if t.verbose {
		fmt.Printf("TransactionExplainer.Process: Starting with %d baggage items\n", len(baggage))
	}
	
	// Clean up baggage first - remove unnecessary data before processing
	t.cleanupBaggage(baggage)

	// Add decoded data for the explanation generation
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

	// TRUE RAG APPROACH: Collect lightweight context only
	// The LLM will autonomously decide what detailed knowledge to retrieve
	var lightweightContext []string
	if contextProviders, ok := baggage["context_providers"].([]interface{}); ok {
		for _, provider := range contextProviders {
			// Try both unified Tool interface and legacy ContextProvider
			if contextProvider, ok := provider.(interface {
				GetPromptContext(context.Context, map[string]interface{}) string
			}); ok {
				if context := contextProvider.GetPromptContext(ctx, baggage); context != "" {
					lightweightContext = append(lightweightContext, context)
				}
			}
		}

		// Generate explanation with autonomous RAG function calling
		explanation, err := t.generateExplanation(ctx, decodedData, baggage, lightweightContext)
		if err != nil {
			return fmt.Errorf("failed to generate explanation with RAG: %w", err)
		}

		// Add explanation to baggage
		baggage["explanation"] = explanation
	} else {
		// This should never happen - all explanations use RAG now
		return fmt.Errorf("no context providers found - cannot generate explanation")
	}

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

	// Generate explanation using LLM with RAG
	explanation, err := t.generateExplanation(ctx, decodedData, make(map[string]interface{}), []string{})
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

// GetPromptContext provides lightweight context for other tools
func (t *TransactionExplainer) GetPromptContext(ctx context.Context, baggage map[string]interface{}) string {
	var contextParts []string

	// Get all context from tools
	if contextProviders, ok := baggage["context_providers"].([]interface{}); ok {
		if t.verbose {
			fmt.Printf("TransactionExplainer.GetPromptContext: Found %d context providers\n", len(contextProviders))
		}
		for i, provider := range contextProviders {
			if contextProvider, ok := provider.(interface {
				GetPromptContext(context.Context, map[string]interface{}) string
			}); ok {
				context := contextProvider.GetPromptContext(ctx, baggage)
				if t.verbose {
					fmt.Printf("TransactionExplainer.GetPromptContext: Provider[%d] (%T): Context length = %d\n", i, contextProvider, len(context))
				}
				if context != "" {
					contextParts = append(contextParts, context)
				}
			} else {
				if t.verbose {
					fmt.Printf("TransactionExplainer.GetPromptContext: Provider[%d] (%T) does not implement GetPromptContext interface\n", i, provider)
				}
			}
		}
	} else {
		if t.verbose {
			fmt.Println("TransactionExplainer.GetPromptContext: No context_providers found in baggage!")
		}
	}

	result := strings.Join(contextParts, "\n\n")
	if t.verbose {
		fmt.Printf("TransactionExplainer.GetPromptContext: Combined context length = %d\n", len(result))
	}

	return result
}

func (t *TransactionExplainer) GetRagContext(ctx context.Context, baggage map[string]interface{}) *RagContext {
	ragContext := NewRagContext()
	return ragContext
}

// generateExplanation generates explanation using autonomous LLM function calling with RAG
func (t *TransactionExplainer) generateExplanation(ctx context.Context, decodedData *models.DecodedData, baggage map[string]interface{}, lightweightContext []string) (*models.ExplanationResult, error) {
	// Build the prompt with lightweight context and RAG instructions
	prompt := t.buildRAGEnabledPrompt(decodedData, lightweightContext)

	if t.verbose {
		fmt.Println("=== TRANSACTION EXPLAINER: TRUE RAG PROMPT ===")
		fmt.Println(prompt)
		fmt.Println("=== END OF PROMPT ===")
		fmt.Println()
	}

	// Get RAG function tools for autonomous searching
	ragTools := t.ragService.GetLangChainGoTools()

	// Call LLM with function calling enabled - THE LLM DECIDES WHAT TO SEARCH
	response, err := t.llm.GenerateContent(ctx, []llms.MessageContent{
		{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{
				llms.TextPart(prompt),
			},
		},
	}, llms.WithTools(ragTools), llms.WithToolChoice("auto"))

	if err != nil {
		return nil, fmt.Errorf("LLM call failed: %w", err)
	}

	// Handle LLM response and potential function calls
	return t.processRAGResponse(ctx, response, decodedData, baggage)
}

// processRAGResponse processes LLM response with potential function calls
func (t *TransactionExplainer) processRAGResponse(ctx context.Context, response *llms.ContentResponse, decodedData *models.DecodedData, baggage map[string]interface{}) (*models.ExplanationResult, error) {
	if response == nil || len(response.Choices) == 0 {
		return nil, fmt.Errorf("no response from LLM")
	}

	choice := response.Choices[0]

	// Check if LLM wants to call functions
	if len(choice.ToolCalls) > 0 {
		if t.verbose {
			fmt.Printf("=== LLM REQUESTED %d FUNCTION CALLS ===\n", len(choice.ToolCalls))
		}

		// Process all function calls
		var functionMessages []llms.MessageContent

		// First, add the original human message to maintain conversation context
		functionMessages = append(functionMessages, llms.MessageContent{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{
				llms.TextPart(t.buildRAGEnabledPrompt(decodedData, []string{})), // Rebuild the original prompt
			},
		})

		// Add the assistant's response with tool calls
		assistantParts := []llms.ContentPart{}
		if choice.Content != "" {
			assistantParts = append(assistantParts, llms.TextPart(choice.Content))
		}
		for _, toolCall := range choice.ToolCalls {
			assistantParts = append(assistantParts, llms.ToolCall{
				ID:   toolCall.ID,
				Type: "function",
				FunctionCall: &llms.FunctionCall{
					Name:      toolCall.FunctionCall.Name,
					Arguments: toolCall.FunctionCall.Arguments,
				},
			})
		}
		
		functionMessages = append(functionMessages, llms.MessageContent{
			Role:  llms.ChatMessageTypeAI,
			Parts: assistantParts,
		})

		// Execute each function call
		for _, toolCall := range choice.ToolCalls {
			if t.verbose {
				fmt.Printf("Executing function: %s with args: %s\n", toolCall.FunctionCall.Name, toolCall.FunctionCall.Arguments)
			}

			// Parse function arguments
			var args map[string]interface{}
			if err := json.Unmarshal([]byte(toolCall.FunctionCall.Arguments), &args); err != nil {
				return nil, fmt.Errorf("failed to parse function arguments: %w", err)
			}

			// Execute the RAG search function - ALWAYS CONTINUE EVEN IF SEARCH FAILS
			result, err := t.ragService.HandleFunctionCall(ctx, toolCall.FunctionCall.Name, args)
			if err != nil {
				// Log the error but don't fail the entire explanation
				if t.verbose {
					fmt.Printf("RAG function call failed (continuing anyway): %v\n", err)
				}
				// Return empty result to continue processing
				result = map[string]interface{}{
					"query":   fmt.Sprintf("%v", args),
					"results": []interface{}{},
					"found":   0,
					"error":   err.Error(),
				}
			}

			// Convert result to JSON for LLM
			resultJSON, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return nil, fmt.Errorf("failed to marshal function result: %w", err)
			}

			// Add function result message
			functionMessages = append(functionMessages, llms.MessageContent{
				Role: llms.ChatMessageTypeTool,
				Parts: []llms.ContentPart{
					llms.ToolCallResponse{
						ToolCallID: toolCall.ID,
						Content:    string(resultJSON),
					},
				},
			})
		}

		if t.verbose {
			fmt.Println("=== SENDING FUNCTION RESULTS BACK TO LLM ===")
		}

		// Send function results back to LLM for final response
		finalResponse, err := t.llm.GenerateContent(ctx, functionMessages)
		if err != nil {
			return nil, fmt.Errorf("failed to get final response after function calls: %w", err)
		}

		// Parse the final response
		if finalResponse != nil && len(finalResponse.Choices) > 0 {
			responseText := finalResponse.Choices[0].Content

			if t.verbose {
				fmt.Println("=== TRANSACTION EXPLAINER: FINAL RAG-ENHANCED RESPONSE ===")
				fmt.Println(responseText)
				fmt.Println("=== END OF RESPONSE ===")
				fmt.Println()
			}

			return t.parseExplanationResponse(ctx, responseText, decodedData, baggage, baggage), nil
		}
	} else {
		// No function calls - process direct response
		responseText := choice.Content

		if t.verbose {
			fmt.Println("=== TRANSACTION EXPLAINER: DIRECT RESPONSE (NO RAG CALLS) ===")
			fmt.Println(responseText)
			fmt.Println("=== END OF RESPONSE ===")
			fmt.Println()
		}

		return t.parseExplanationResponse(ctx, responseText, decodedData, baggage, baggage), nil
	}

	return nil, fmt.Errorf("no valid response from LLM")
}

// buildRAGEnabledPrompt creates a prompt that encourages autonomous RAG usage
func (t *TransactionExplainer) buildRAGEnabledPrompt(decodedData *models.DecodedData, lightweightContext []string) string {
	prompt := `You are a blockchain transaction analyzer with autonomous search capabilities. Your task is to provide a VERY SHORT, precise summary that includes ALL critical transaction details.

REQUIRED FORMAT: [ACTION] [PROTOCOL/CONTRACT] [SPECIFIC AMOUNTS] [TOKEN SYMBOLS] [KEY ADDRESSES] + [GAS FEE]

EXAMPLES OF PERFECT FORMAT (covering diverse transaction types):
- "Approved 🍣 SushiSwap router to spend unlimited PEPE tokens for 0x3286...399f (outta.eth) + $0.85 gas"
- "Swapped 100 USDT for 0.0264 WETH via 1inch aggregator + $1.20 gas"
- "Transferred 57,071 GrowAI tokens to 0x1234...5678 + $0.82 gas"
- "Minted 5 NFTs from CryptoPunks to 0xabcd...ef01 (vitalik.eth) + $2.10 gas"
- "Granted role #7 to 0x1234...5678 (alice.eth) on access control contract + $0.45 gas"
- "Voted on proposal #12 in DAO governance contract for 0x9876...cdef (dao-member.eth) + $0.62 gas"
- "Deployed new contract 0xabcd...1234 by 0x5678...9abc (deployer.eth) + $1.85 gas"
- "Updated user permissions on contract 0x2345...6789 by 0x1111...2222 (admin.eth) + $0.38 gas"

CRITICAL REQUIREMENTS - INCLUDE ALL OF THESE (when applicable):
1. **SPECIFIC ACTION**: Approved, Swapped, Transferred, Minted, Staked, Granted, Voted, Deployed, Updated, etc.
2. **EXACT AMOUNTS**: Include all relevant token amounts (100 USDT, 0.0264 WETH, unlimited, etc.) OR role numbers/IDs
3. **TOKEN SYMBOLS**: Use actual symbols (USDT, WETH, PEPE) - BUT ONLY if tokens are actually involved
4. **PROTOCOL NAMES**: Use discovered protocol names (SushiSwap, 1inch, Uniswap) with emojis if available
5. **KEY ADDRESSES**: Include recipient/sender addresses in shortened format (0x1234...5678)
6. **ENS NAMES**: Add ENS names in parentheses when available (vitalik.eth)
7. **GAS FEE**: Always end with "+ $X.XX gas" 

TRANSACTION TYPE DETECTION:
- **TOKEN TRANSACTIONS**: Look for Transfer events, token method calls (transfer, approve, etc.)
- **ROLE MANAGEMENT**: Look for RoleGranted, RoleRevoked, UserRoleUpdated events with role parameters
- **GOVERNANCE**: Look for Vote, Proposal, Delegation events and DAO-related activities
- **CONTRACT DEPLOYMENT**: Look for contract creation, zero address recipients
- **ACCESS CONTROL**: Look for permission updates, admin changes, access modifications
- **NFT OPERATIONS**: Look for ERC721/ERC1155 Transfer events with tokenId parameters

AUTONOMOUS SEARCH INSTRUCTIONS:
- When you encounter UNKNOWN protocols, contracts, or addresses, USE the search functions available to you
- Call search_protocols() when you see contract addresses you don't recognize that might be protocols
- Call search_tokens() when you see token addresses or symbols you need more information about  
- Call search_addresses() when you see addresses that might be well-known entities
- The search functions do fuzzy matching, so partial matches work well
- You can make multiple searches as needed to fully understand the transaction

CRITICAL CONTINUATION RULE:
- ALWAYS provide a final explanation, even if some searches return no results
- If a search finds nothing, simply continue your analysis without that information
- DO NOT stop or ask for more context - analyze what you can see and provide the best summary possible
- NEVER ask the user to provide transaction details or raw data - work with what you have
- If you can't find complete information, provide the best explanation possible with available details
- The goal is ALWAYS to reach a final transaction explanation, regardless of search results

CRITICAL ROLE DATA RECOGNITION:
- UserRoleUpdated, RoleGranted, RoleRevoked events ARE role data - use their parameters directly
- Events with role/permission parameters contain the role information you need
- Do NOT claim "no role data" when role-related events are present in the context
- Extract role numbers, enabled/disabled status, and user addresses from these events

## Transaction Analysis:`

	// Add lightweight context from other tools (events, calls, etc. already provided by relevant tools)
	if len(lightweightContext) > 0 {
		prompt += "\n\n### ADDITIONAL CONTEXT:\n"
		for _, context := range lightweightContext {
			prompt += context + "\n\n"
		}
	}

	prompt += `

## INSTRUCTIONS:

1. **AUTONOMOUS SEARCHING**: When you see unknown contracts, tokens, or addresses, immediately search for them using the available functions
2. **BE SPECIFIC**: Use exact numbers, amounts, and names from your searches
3. **CONCISE OUTPUT**: Keep the final explanation under 30 words - users read it in 2 seconds
4. **SEARCH FIRST, EXPLAIN SECOND**: Gather all needed information through searches, then provide the final explanation

EXAMPLE WORKFLOW FOR TOKEN TRANSACTION:
1. See unknown contract 0x111111125... → search_protocols("0x111111125")
2. Find it's "1inch Aggregation Router v6" → now you know it's a DEX aggregator  
3. See token 0xa0b86a33... → search_tokens("0xa0b86a33")
4. Find it's "Chainlink Token (LINK)" → now you have token details
5. Provide final explanation: "Swapped 100 LINK for 0.5 ETH via 1inch aggregator + $1.20 gas"

EXAMPLE WORKFLOW FOR ROLE MANAGEMENT:
1. See UserRoleUpdated event with role parameter 7, enabled=true → extract role data directly from event
2. See transaction initiator with ENS name → include ENS in explanation  
3. See unknown contract 0x123... → search_protocols("0x123") if needed
4. Find no protocol results → continue with "access control contract" description using available event data
5. Provide final explanation: "Granted role #7 to 0x000...000 by 0x7e97...63c7 (cocytus.eth) on access control contract + $0.75 gas"

EXAMPLE WITH NO SEARCH RESULTS:
1. See unknown contract 0x123... → search_protocols("0x123")
2. Search returns no results → continue anyway
3. See unknown operation → analyze events and parameters
4. Provide final explanation: "Updated contract permissions on 0x123...456 by 0x789...abc + $0.82 gas"

FORMATTING RULES:
- Use EXACT token amounts from context (100 USDT, not "some USDT") - BUT ONLY if tokens exist
- Use EXACT role numbers/IDs from event parameters (role #7, not "some role")
- Use EXACT token symbols from context (PEPE, WETH, not "tokens") - BUT ONLY if tokens exist
- Use SHORTENED addresses (0x1234...5678 format)
- Include ENS names when available: 0x1234...5678 (vitalik.eth)
- Always end with gas fee: + $X.XX gas
- Use protocol emojis when known (🍣 for SushiSwap, 🦄 for Uniswap)
- Be specific about actions: Approved, Swapped, Transferred, Granted, Revoked, Updated, Deployed, etc.

**CRITICAL RULE**: Do NOT assume this is a token transaction unless you see clear evidence (Transfer events, token method calls). Many blockchain transactions are about governance, access control, contract management, or other non-token operations.

**ABSOLUTELY FORBIDDEN RESPONSES**: 
- NEVER say "No known protocol found" and stop analyzing
- NEVER say "Without token or role data" when events contain role information  
- NEVER say "Please provide transaction details or raw data for analysis"
- NEVER ask the user for more information - analyze what you have

Search for anything you don't immediately recognize, but ALWAYS provide your concise, data-rich explanation regardless of search results.`

	return prompt
}
