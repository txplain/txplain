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
func NewTransactionExplainer(llm llms.Model, staticProvider *StaticContextProvider, verbose bool) *TransactionExplainer {
	ragService := NewRAGSearchService(staticProvider, verbose)
	return &TransactionExplainer{
		llm:        llm,
		ragService: ragService,
		verbose:    verbose,
	}
}

// Dependencies returns the tools this processor depends on
func (t *TransactionExplainer) Dependencies() []string {
	return []string{
		"abi_resolver", "log_decoder", "trace_decoder", "ens_resolver",
		"token_metadata_enricher", "erc20_price_lookup", "monetary_value_enricher",
		"address_role_resolver", "protocol_resolver", "tag_resolver", "static_context_provider",
	}
}

// Process generates a human-readable explanation of the transaction
func (t *TransactionExplainer) Process(ctx context.Context, baggage map[string]interface{}) error {
	if t.verbose {
		fmt.Printf("TransactionExplainer.Process: Starting with %d baggage items\n", len(baggage))
	}

	// Get progress tracker from baggage if available
	progressTracker, hasProgress := baggage["progress_tracker"].(*models.ProgressTracker)

	// Send initial progress update
	if hasProgress {
		progressTracker.UpdateComponent("transaction_explainer", models.ComponentGroupAnalysis, "AI Analysis", models.ComponentStatusRunning, "Preparing transaction data for AI analysis...")
	}

	// Clean up baggage first - remove unnecessary data before processing
	t.cleanupBaggage(baggage)

	// Send progress update for data preparation
	if hasProgress {
		progressTracker.UpdateComponent("transaction_explainer", models.ComponentGroupAnalysis, "AI Analysis", models.ComponentStatusRunning, "Building context for AI analysis...")
	}

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

	// Send progress update for context collection
	if hasProgress {
		progressTracker.UpdateComponent("transaction_explainer", models.ComponentGroupAnalysis, "AI Analysis", models.ComponentStatusRunning, "Collecting context from analysis tools...")
	}

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

		// Send progress update for starting AI generation
		if hasProgress {
			progressTracker.UpdateComponent("transaction_explainer", models.ComponentGroupAnalysis, "AI Analysis", models.ComponentStatusRunning, "Starting AI explanation generation...")
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
		// Try to get the actual raw transaction data (it might be nested)
		var actualRawData map[string]interface{}
		if nestedRawData, ok := rawData["raw_data"].(map[string]interface{}); ok {
			actualRawData = nestedRawData
		} else {
			actualRawData = rawData
		}

		if networkID, ok := actualRawData["network_id"].(float64); ok {
			result.NetworkID = int64(networkID)
		}
		if txHash, ok := actualRawData["tx_hash"].(string); ok {
			result.TxHash = txHash
		}

		// Extract transaction details from receipt
		if receipt, ok := actualRawData["receipt"].(map[string]interface{}); ok {
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

	// Extract token transfers from baggage (set by TokenTransferExtractor)
	if transfers, ok := baggage["transfers"].([]models.TokenTransfer); ok {
		result.Transfers = transfers
	} else {
		result.Transfers = []models.TokenTransfer{} // Empty if no transfers found
	}

	// Get tags from tag resolver (probabilistic approach)
	if tags, ok := baggage["tags"].([]string); ok {
		result.Tags = tags
	} else {
		result.Tags = []string{} // Empty if tag resolver didn't run
	}

	// Generate AI-enhanced links with meaningful labels
	result.Links = t.generateIntelligentLinks(ctx, result.TxHash, result.NetworkID, baggage)

	// Extract participants from address_role_resolver
	if participants, ok := baggage["address_participants"].([]models.AddressParticipant); ok {
		result.Participants = participants
	} else {
		result.Participants = []models.AddressParticipant{} // Empty if no participants found
	}

	// Add address categories to metadata for frontend legend grouping (backward compatibility)
	if len(result.Participants) > 0 {
		// Create categories map for frontend
		categories := make(map[string][]map[string]string)

		// Group participants by category
		for _, participant := range result.Participants {
			if participant.Role != "" && participant.Category != "" {
				// Initialize category array if not exists
				if categories[participant.Category] == nil {
					categories[participant.Category] = make([]map[string]string, 0)
				}

				// Add participant to category
				categories[participant.Category] = append(categories[participant.Category], map[string]string{
					"address": participant.Address,
					"role":    participant.Role,
				})
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

// generateIntelligentLinks creates explorer links using participant data from address_role_resolver
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

	// Get participants from address_role_resolver
	participants, ok := baggage["address_participants"].([]models.AddressParticipant)
	if !ok || len(participants) == 0 {
		// Fallback to simple contract links if no participants found
		return t.generateFallbackLinks(txHash, networkID, baggage)
	}

	// Create links with meaningful role-based labels using participant data
	for _, participant := range participants {
		if participant.Address != "" && participant.Role != "" {
			links[participant.Role] = fmt.Sprintf("%s/address/%s", network.Explorer, participant.Address)
		}
	}

	return links
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
	// Get progress tracker from baggage if available
	progressTracker, hasProgress := baggage["progress_tracker"].(*models.ProgressTracker)

	// Build the prompt with lightweight context and RAG instructions
	prompt := t.buildRAGEnabledPrompt(decodedData, lightweightContext)

	if t.verbose {
		fmt.Println("=== TRANSACTION EXPLAINER: TRUE RAG PROMPT ===")
		fmt.Println(prompt)
		fmt.Println("=== END OF PROMPT ===")
		fmt.Println()
	}

	// Send progress update for calling AI
	if hasProgress {
		progressTracker.UpdateComponent("transaction_explainer", models.ComponentGroupAnalysis, "AI Analysis", models.ComponentStatusRunning, "Sending transaction data to AI...")
	}

	// Get RAG function tools for autonomous searching
	ragTools := t.ragService.GetLangChainGoTools()

	// Call LLM with function calling enabled and retry logic - THE LLM DECIDES WHAT TO SEARCH
	response, err := CallLLMWithRetry(ctx, t.llm, []llms.MessageContent{
		{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{
				llms.TextPart(prompt),
			},
		},
	}, t.verbose, llms.WithTools(ragTools), llms.WithToolChoice("auto"))

	if err != nil {
		// Update progress tracker to show the error before returning
		if hasProgress {
			progressTracker.UpdateComponent("transaction_explainer", models.ComponentGroupAnalysis, "AI Analysis", models.ComponentStatusRunning, fmt.Sprintf("AI call failed: %v", err))
		}
		return nil, fmt.Errorf("LLM call failed: %w", err)
	}

	// Send progress update for processing AI response
	if hasProgress {
		progressTracker.UpdateComponent("transaction_explainer", models.ComponentGroupAnalysis, "AI Analysis", models.ComponentStatusRunning, "Processing AI response...")
	}

	// Handle LLM response and potential function calls
	return t.processRAGResponse(ctx, response, decodedData, baggage, lightweightContext)
}

// processRAGResponse processes LLM response with potential function calls
func (t *TransactionExplainer) processRAGResponse(ctx context.Context, response *llms.ContentResponse, decodedData *models.DecodedData, baggage map[string]interface{}, lightweightContext []string) (*models.ExplanationResult, error) {
	if response == nil || len(response.Choices) == 0 {
		return nil, fmt.Errorf("no response from LLM")
	}

	// Get progress tracker from baggage if available
	progressTracker, hasProgress := baggage["progress_tracker"].(*models.ProgressTracker)

	choice := response.Choices[0]

	// Check if LLM wants to call functions
	if len(choice.ToolCalls) > 0 {
		if t.verbose {
			fmt.Printf("=== LLM REQUESTED %d FUNCTION CALLS ===\n", len(choice.ToolCalls))
		}

		// Send progress update for function calls
		if hasProgress {
			progressTracker.UpdateComponent("transaction_explainer", models.ComponentGroupAnalysis, "AI Analysis", models.ComponentStatusRunning, fmt.Sprintf("AI requesting %d knowledge searches...", len(choice.ToolCalls)))
		}

		// Process all function calls
		var functionMessages []llms.MessageContent

		// First, add the original human message to maintain conversation context
		functionMessages = append(functionMessages, llms.MessageContent{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{
				llms.TextPart(t.buildRAGEnabledPrompt(decodedData, lightweightContext)), // Rebuild the original prompt with full context
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
		for i, toolCall := range choice.ToolCalls {
			// Send progress update for each function call
			if hasProgress {
				progressTracker.UpdateComponent("transaction_explainer", models.ComponentGroupAnalysis, "AI Analysis", models.ComponentStatusRunning, fmt.Sprintf("Executing search %d/%d: %s", i+1, len(choice.ToolCalls), toolCall.FunctionCall.Name))
			}

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

		// Send progress update for final AI generation
		if hasProgress {
			progressTracker.UpdateComponent("transaction_explainer", models.ComponentGroupAnalysis, "AI Analysis", models.ComponentStatusRunning, "Generating final explanation...")
		}

		// Add final instruction to ensure LLM provides the explanation
		functionMessages = append(functionMessages, llms.MessageContent{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{
				llms.TextPart("Now provide the final transaction explanation in the required format based on the search results and transaction context above."),
			},
		})

		if t.verbose {
			fmt.Println("=== SENDING FUNCTION RESULTS BACK TO LLM ===")
		}

		// Send function results back to LLM for final response with retry logic
		finalResponse, err := CallLLMWithRetry(ctx, t.llm, functionMessages, t.verbose)
		if err != nil {
			// Update progress tracker to show the error before returning
			if hasProgress {
				progressTracker.UpdateComponent("transaction_explainer", models.ComponentGroupAnalysis, "AI Analysis", models.ComponentStatusRunning, fmt.Sprintf("Final AI call failed: %v", err))
			}
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
	prompt := `You are a blockchain transaction analyzer with autonomous search capabilities. Your task is to provide a creative but comprehensive explanation that tells the complete story of what happened in this transaction.

## CREATIVE EXPLANATION REQUIREMENTS

**BE CREATIVE WITH SENTENCE STRUCTURE** - Write natural, flowing sentences that tell the complete transaction story, but ENSURE every critical detail is included.

**PERFECT EXAMPLES** (showing creative but complete explanations):
- "Swapped 100 USDT for 55,227 GrowAI tokens by first converting to 0.0264 WETH via 1inch aggregator, received by 0x39e5...09c5 + $1.47 gas"
- "Approved 🍣 SushiSwap router to spend unlimited PEPE tokens from 0x3286...399f (outta.eth), preparing for future trades + $0.85 gas"
- "Transferred 57,071 GrowAI tokens from 0x1234...5678 to 0x9876...4321 (alice.eth) in a single direct transfer + $0.82 gas"
- "Minted 3 new CryptoPunks NFTs (tokens #1205, #1206, #1207) directly to collector 0xabcd...ef01 (vitalik.eth) + $2.10 gas"
- "Granted admin role #7 to 0x2222...3333 (dao-member.eth) on governance contract 0x1111...2222, expanding permissions + $0.45 gas"
- "Deposited 2.5 ETH into Compound lending pool, receiving 125.8 cETH tokens as collateral proof to 0x5555...6666 + $1.20 gas"
- "Executed multi-step arbitrage: bought 1000 LINK with ETH on Uniswap, then sold for 1050 LINK on SushiSwap, netting 50 LINK profit + $3.40 gas"

**MANDATORY DATA POINTS** - Your creative explanation MUST include ALL applicable details:

1. **EXACT ACTION & FLOW**: Describe what happened step-by-step if complex (swapped X for Y by first converting to Z)
2. **PRECISE AMOUNTS**: Every token amount, count, or quantity mentioned in events/transfers
3. **TOKEN SYMBOLS & NAMES**: Use actual discovered symbols (USDT, GrowAI, WETH) - only when tokens are involved
4. **PROTOCOL IDENTIFICATION**: Use discovered protocol names with emojis when available (🍣 SushiSwap, 1inch, 🦄 Uniswap)
5. **ALL KEY ADDRESSES**: Include sender, recipient, and any intermediate addresses in 0x1234...5678 format
6. **ENS NAMES**: Add ENS names in parentheses when available (vitalik.eth)
7. **TRANSACTION FLOW**: Show intermediate steps in multi-step transactions (converting through WETH, etc.)
8. **RECIPIENT INFORMATION**: Always mention who received what ("received by", "sent to", "transferred to")
9. **GAS FEE**: Always end with "+ $X.XX gas"
10. **CONTEXT CLUES**: Add helpful context like "preparing for future trades", "expanding permissions", "as collateral proof"

**CRITICAL: AVOID TECHNICAL LANGUAGE**: 
- NEVER use technical event names like "RelayNativeDeposit event", "Transfer event", "UserRoleUpdated event"
- Instead, describe what happened in plain English: "deposited to bridge", "transferred tokens", "updated permissions"  
- Use human-friendly language that normal web3 users can understand
- Focus on the action and outcome, not the technical implementation details

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

**EXAMPLE CREATIVE WORKFLOW FOR COMPLEX SWAP**:
1. See unknown contract 0x111111125... → search_protocols("0x111111125")
2. Find it's "1inch Aggregation Router v6" → now you know it's a DEX aggregator  
3. See multiple Transfer events with different tokens → trace the complete flow
4. See token 0xa0b86a33... → search_tokens("0xa0b86a33")
5. Find it's "Chainlink Token (LINK)" → now you have token details
6. Analyze all transfers to understand the complete journey
7. Creative explanation: "Swapped 100 LINK for 2.5 ETH by first converting to 150 USDC via 1inch aggregator, then routing through WETH, finally received by 0x1234...5678 (trader.eth) + $1.20 gas"

**EXAMPLE CREATIVE WORKFLOW FOR ROLE MANAGEMENT**:
1. See UserRoleUpdated event with role parameter 7, enabled=true, user=0x1234...5678
2. See transaction initiator 0x7e97...63c7 with ENS name cocytus.eth
3. See unknown contract 0x123... → search_protocols("0x123") if needed
4. Find no protocol results → continue with available event data
5. Creative explanation: "Granted admin role #7 to new member 0x1234...5678 by governance admin 0x7e97...63c7 (cocytus.eth), expanding DAO permissions on access control contract + $0.75 gas"

**EXAMPLE WITH LIMITED SEARCH RESULTS**:
1. See unknown contract 0x123... → search_protocols("0x123")
2. Search returns minimal results → use available transaction context
3. See permission update events → analyze parameters for roles/addresses
4. Creative explanation: "Updated smart contract permissions for user 0x2222...3333 on governance system 0x123...456, modifying access control settings + $0.82 gas"

**CREATIVE WRITING GUIDELINES**:
- **NATURAL FLOW**: Write like you're explaining to a friend - use connecting words and phrases ("by first converting", "then received", "in order to")
- **COMPLETE STORY**: Include the full transaction journey - show all steps, intermediate conversions, and final destinations
- **EXACT AMOUNTS**: Use precise numbers from context (100 USDT, 55,227 GrowAI, 0.0264 WETH) - never approximate or generalize
- **SPECIFIC TOKENS**: Use actual discovered symbols (PEPE, WETH, GrowAI) with exact names when available - only when tokens are involved
- **DETAILED ADDRESSES**: Include all relevant addresses in 0x1234...5678 format with ENS names: 0x1234...5678 (vitalik.eth)
- **PROTOCOL PERSONALITY**: Use emojis and personality for known protocols (🍣 SushiSwap, 1inch aggregator, 🦄 Uniswap)
- **ACTION SPECIFICITY**: Be precise about actions (Approved unlimited spending, Swapped through aggregator, Transferred directly, Granted admin role)
- **CONTEXTUAL CLUES**: Add helpful interpretation ("preparing for trades", "expanding access", "as collateral", "netting profit")
- **ALWAYS END WITH GAS**: Every explanation must end with + $X.XX gas

**CRITICAL RULE**: Do NOT assume this is a token transaction unless you see clear evidence (Transfer events, token method calls). Many blockchain transactions are about governance, access control, contract management, or other non-token operations.

**ABSOLUTELY FORBIDDEN RESPONSES**: 
- NEVER say "No known protocol found" and stop analyzing
- NEVER say "Without token or role data" when events contain role information  
- NEVER say "Please provide transaction details or raw data for analysis"
- NEVER ask the user for more information - analyze what you have

Search for anything you don't immediately recognize, but ALWAYS provide your concise, data-rich explanation regardless of search results.`

	return prompt
}
