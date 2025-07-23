package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tmc/langchaingo/llms"
	"github.com/txplain/txplain/internal/models"
	"github.com/txplain/txplain/internal/rpc"
)

// AddressRoleResolver analyzes all addresses involved in a transaction to determine their roles, categories, and types
type AddressRoleResolver struct {
	llm       llms.Model
	rpcClient *rpc.Client
	verbose   bool
}

// NewAddressRoleResolver creates a new address role resolver
func NewAddressRoleResolver(llm llms.Model) *AddressRoleResolver {
	return &AddressRoleResolver{
		llm:     llm,
		verbose: false,
	}
}

// SetRPCClient sets the RPC client for address type detection
func (a *AddressRoleResolver) SetRPCClient(client *rpc.Client) {
	a.rpcClient = client
}

// SetVerbose enables or disables verbose logging
func (a *AddressRoleResolver) SetVerbose(verbose bool) {
	a.verbose = verbose
}

// Name returns the tool name
func (a *AddressRoleResolver) Name() string {
	return "address_role_resolver"
}

// Description returns the tool description
func (a *AddressRoleResolver) Description() string {
	return "Analyzes all transaction addresses to determine roles, categories, and types (EOA vs Contract)"
}

// Dependencies returns the tools this processor depends on
func (a *AddressRoleResolver) Dependencies() []string {
	return []string{
		"abi_resolver", "log_decoder", "trace_decoder", "ens_resolver", "token_metadata_enricher",
	}
}

// Process implements the Tool interface
func (a *AddressRoleResolver) Process(ctx context.Context, baggage map[string]interface{}) error {
	if a.verbose {
		fmt.Println("\n" + strings.Repeat("🏠", 60))
		fmt.Println("🔍 ADDRESS ROLE RESOLVER: Starting address role analysis")
		fmt.Println(strings.Repeat("🏠", 60))
	}

	// Step 1: Extract all addresses involved in the transaction
	addresses := a.extractAllAddresses(baggage)
	if len(addresses) == 0 {
		if a.verbose {
			fmt.Println("⚠️  No addresses found in transaction")
			fmt.Println(strings.Repeat("🏠", 60) + "\n")
		}
		baggage["address_participants"] = []models.AddressParticipant{}
		return nil
	}

	if a.verbose {
		fmt.Printf("📊 Found %d unique addresses to analyze\n", len(addresses))
		for i, addr := range addresses {
			fmt.Printf("   %d. %s\n", i+1, addr)
		}
	}

	// Step 2: Determine address types (EOA vs Contract)
	addressTypes, err := a.determineAddressTypes(ctx, addresses)
	if err != nil {
		if a.verbose {
			fmt.Printf("❌ Failed to determine address types: %v\n", err)
			fmt.Println(strings.Repeat("🏠", 60) + "\n")
		}
		return fmt.Errorf("failed to determine address types: %w", err)
	}

	if a.verbose {
		fmt.Println("🏛️  Address types determined:")
		for addr, addrType := range addressTypes {
			fmt.Printf("   %s: %s\n", addr, addrType)
		}
	}

	// Step 3: Use AI to infer roles and categories
	roleData, err := a.inferAddressRoles(ctx, baggage, addresses)
	if err != nil {
		if a.verbose {
			fmt.Printf("❌ Failed to infer address roles: %v\n", err)
			fmt.Println(strings.Repeat("🏠", 60) + "\n")
		}
		return fmt.Errorf("failed to infer address roles: %w", err)
	}

	if a.verbose {
		fmt.Println("🎭 Address roles inferred:")
		for addr, role := range roleData {
			fmt.Printf("   %s: %s (%s)\n", addr, role["role"], role["category"])
		}
	}

	// Step 4: Get network info for explorer links
	networkID := int64(1) // Default to Ethereum
	if nid, ok := baggage["raw_data"].(map[string]interface{})["network_id"].(float64); ok {
		networkID = int64(nid)
	}

	network, exists := models.GetNetwork(networkID)
	if !exists {
		if a.verbose {
			fmt.Printf("⚠️  Unknown network ID: %d\n", networkID)
		}
	}

	// Step 5: Enrich with additional context data
	participants := a.buildParticipants(addresses, addressTypes, roleData, baggage, network)

	if a.verbose {
		fmt.Printf("✅ Built %d participants with full context\n", len(participants))
		for i, p := range participants {
			fmt.Printf("   %d. %s: %s (%s) [%s]\n", i+1, p.Address, p.Role, p.Category, p.Type)
		}
		fmt.Println(strings.Repeat("🏠", 60) + "\n")
	}

	// Store results in baggage for other tools
	baggage["address_participants"] = participants
	baggage["address_roles"] = roleData // Keep legacy format for backward compatibility

	return nil
}

// extractAllAddresses collects all unique addresses from various transaction data sources
func (a *AddressRoleResolver) extractAllAddresses(baggage map[string]interface{}) []string {
	addressMap := make(map[string]bool)

	// Extract from raw transaction data
	if rawData, ok := baggage["raw_data"].(map[string]interface{}); ok {
		// Transaction sender/receiver
		if receipt, ok := rawData["receipt"].(map[string]interface{}); ok {
			if from, ok := receipt["from"].(string); ok && from != "" && from != "0x" {
				addressMap[strings.ToLower(from)] = true
			}
			if to, ok := receipt["to"].(string); ok && to != "" && to != "0x" {
				addressMap[strings.ToLower(to)] = true
			}
		}
	}

	// Extract from contract addresses (detected by ABI resolver)
	if contractAddresses, ok := baggage["contract_addresses"].([]string); ok {
		for _, addr := range contractAddresses {
			if addr != "" && addr != "0x" {
				addressMap[strings.ToLower(addr)] = true
			}
		}
	}

	// Extract from events
	if events, ok := baggage["events"].([]models.Event); ok {
		for _, event := range events {
			if event.Contract != "" && event.Contract != "0x" {
				addressMap[strings.ToLower(event.Contract)] = true
			}

			// Extract from event parameters
			if event.Parameters != nil {
				for _, value := range event.Parameters {
					if addr, ok := value.(string); ok && a.isValidAddress(addr) {
						addressMap[strings.ToLower(addr)] = true
					}
				}
			}
		}
	}

	// Extract from calls (trace data)
	if calls, ok := baggage["calls"].([]models.Call); ok {
		for _, call := range calls {
			if call.Contract != "" && call.Contract != "0x" {
				addressMap[strings.ToLower(call.Contract)] = true
			}

			// Extract from call arguments
			if call.Arguments != nil {
				for _, value := range call.Arguments {
					if addr, ok := value.(string); ok && a.isValidAddress(addr) {
						addressMap[strings.ToLower(addr)] = true
					}
				}
			}
		}
	}

	// Extract from transfers
	if transfers, ok := baggage["transfers"].([]models.TokenTransfer); ok {
		for _, transfer := range transfers {
			if transfer.From != "" && transfer.From != "0x" {
				cleanFrom := a.cleanAddress(transfer.From)
				if cleanFrom != "" {
					addressMap[strings.ToLower(cleanFrom)] = true
				}
			}
			if transfer.To != "" && transfer.To != "0x" {
				cleanTo := a.cleanAddress(transfer.To)
				if cleanTo != "" {
					addressMap[strings.ToLower(cleanTo)] = true
				}
			}
			if transfer.Contract != "" && transfer.Contract != "0x" {
				addressMap[strings.ToLower(transfer.Contract)] = true
			}
		}
	}

	// Extract from token metadata
	if tokenMetadata, ok := baggage["token_metadata"].(map[string]*TokenMetadata); ok {
		for addr := range tokenMetadata {
			if addr != "" && addr != "0x" {
				addressMap[strings.ToLower(addr)] = true
			}
		}
	}

	// Convert map to slice
	var addresses []string
	for addr := range addressMap {
		// Final validation and ensure proper format
		if len(addr) == 42 && strings.HasPrefix(addr, "0x") {
			addresses = append(addresses, addr)
		}
	}

	return addresses
}

// isValidAddress checks if a string looks like a valid Ethereum address
func (a *AddressRoleResolver) isValidAddress(addr string) bool {
	if len(addr) != 42 {
		return false
	}
	if !strings.HasPrefix(addr, "0x") {
		return false
	}
	// Basic hex validation
	for _, char := range addr[2:] {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}

// cleanAddress removes padding and validates address format
func (a *AddressRoleResolver) cleanAddress(addr string) string {
	if addr == "" {
		return ""
	}

	// Remove 0x prefix for processing
	if strings.HasPrefix(addr, "0x") {
		addr = addr[2:]
	}

	// Remove leading zeros (common in event parameters)
	addr = strings.TrimLeft(addr, "0")

	// Handle case where address is all zeros
	if addr == "" {
		return "0x0000000000000000000000000000000000000000"
	}

	// Pad back to 40 characters
	for len(addr) < 40 {
		addr = "0" + addr
	}

	// Re-add 0x prefix
	result := "0x" + addr

	// Validate final format
	if a.isValidAddress(result) {
		return result
	}

	return ""
}

// determineAddressTypes determines if each address is an EOA or Contract using RPC calls
func (a *AddressRoleResolver) determineAddressTypes(ctx context.Context, addresses []string) (map[string]string, error) {
	addressTypes := make(map[string]string)

	if a.rpcClient == nil {
		// Default to unknown if no RPC client available
		for _, addr := range addresses {
			addressTypes[addr] = "Unknown"
		}
		return addressTypes, nil
	}

	for _, address := range addresses {
		// Check if address has code (contract) or not (EOA)
		code, err := a.rpcClient.GetCode(ctx, address)
		if err != nil {
			if a.verbose {
				fmt.Printf("⚠️  Failed to get code for %s: %v\n", address, err)
			}
			addressTypes[address] = "Unknown"
			continue
		}

		// If code exists and is not empty (0x), it's a contract
		if code != "" && code != "0x" && code != "0x0" {
			addressTypes[address] = "Contract"
		} else {
			addressTypes[address] = "EOA"
		}
	}

	return addressTypes, nil
}

// inferAddressRoles uses AI to infer meaningful roles for addresses
func (a *AddressRoleResolver) inferAddressRoles(ctx context.Context, baggage map[string]interface{}, addresses []string) (map[string]map[string]string, error) {
	// Build context for AI analysis
	prompt := a.buildAddressRolePrompt(baggage, addresses)

	if a.verbose {
		fmt.Println("=== ADDRESS ROLE INFERENCE: PROMPT ===")
		fmt.Println(prompt)
		fmt.Println("=== END PROMPT ===")
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

	responseText := ""
	if response != nil && len(response.Choices) > 0 {
		responseText = response.Choices[0].Content
	}

	if a.verbose {
		fmt.Println("=== ADDRESS ROLE INFERENCE: LLM RESPONSE ===")
		fmt.Println(responseText)
		fmt.Println("=== END RESPONSE ===")
		fmt.Println()
	}

	// Parse the response
	return a.parseAddressRoleResponse(responseText)
}

// buildAddressRolePrompt creates the prompt for AI address role inference
func (a *AddressRoleResolver) buildAddressRolePrompt(baggage map[string]interface{}, addresses []string) string {
	networkID := int64(1) // Default to Ethereum
	if nid, ok := baggage["raw_data"].(map[string]interface{})["network_id"].(float64); ok {
		networkID = int64(nid)
	}

	prompt := `You are a blockchain transaction analyst. Analyze this transaction and identify the role of each address/contract involved, AND categorize them into groups. Provide meaningful labels that help users understand what each address represents in the context of this specific transaction.

ADDRESSES TO ANALYZE:`
	for i, addr := range addresses {
		prompt += fmt.Sprintf("\n%d. %s", i+1, addr)
	}

	prompt += "\n\nTRANSACTION CONTEXT:"

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

	// Add events context
	if events, ok := baggage["events"].([]models.Event); ok && len(events) > 0 {
		prompt += "\n\nEVENTS:"
		for _, event := range events {
			eventInfo := fmt.Sprintf("%s event on %s", event.Name, event.Contract)

			// Include ALL event parameters generically
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

	// Add calls context
	if calls, ok := baggage["calls"].([]models.Call); ok && len(calls) > 0 {
		prompt += "\n\nCONTRACT CALLS:"
		for _, call := range calls {
			callInfo := fmt.Sprintf("%s.%s() on %s", call.Contract, call.Method, call.Contract)
			if call.Value != "" && call.Value != "0" && call.Value != "0x0" {
				callInfo += fmt.Sprintf(" [Value: %s ETH]", call.Value)
			}
			prompt += fmt.Sprintf("\n- %s", callInfo)
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

	// Add ENS names context
	if ensNames, ok := baggage["ens_names"].(map[string]string); ok && len(ensNames) > 0 {
		prompt += "\n\nENS NAMES:"
		for addr, name := range ensNames {
			prompt += fmt.Sprintf("\n- %s: %s", addr, name)
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
5. Include ALL addresses provided above - don't skip any

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

Analyze the transaction context and identify meaningful roles and categories for ALL addresses listed above:
`

	return prompt
}

// parseAddressRoleResponse parses the LLM response into address-role mappings with categories
func (a *AddressRoleResolver) parseAddressRoleResponse(response string) (map[string]map[string]string, error) {
	response = strings.TrimSpace(response)

	// Look for JSON object
	jsonStart := strings.Index(response, "{")
	jsonEnd := strings.LastIndex(response, "}")

	if jsonStart == -1 || jsonEnd == -1 || jsonEnd <= jsonStart {
		return nil, fmt.Errorf("no valid JSON object found in response")
	}

	jsonStr := response[jsonStart : jsonEnd+1]

	// Parse JSON with the format: address -> {role, category}
	var addressRoles map[string]map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &addressRoles); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	// Convert to string-string map and validate
	cleaned := make(map[string]map[string]string)
	for address, roleData := range addressRoles {
		address = strings.TrimSpace(strings.ToLower(address))
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

// buildParticipants creates AddressParticipant objects with full context
func (a *AddressRoleResolver) buildParticipants(addresses []string, addressTypes map[string]string, roleData map[string]map[string]string, baggage map[string]interface{}, network models.Network) []models.AddressParticipant {
	var participants []models.AddressParticipant

	// Get ENS names
	ensNames := make(map[string]string)
	if names, ok := baggage["ens_names"].(map[string]string); ok {
		ensNames = names
	}

	// Get token metadata for names and icons
	tokenMetadata := make(map[string]*TokenMetadata)
	if metadata, ok := baggage["token_metadata"].(map[string]*TokenMetadata); ok {
		tokenMetadata = metadata
	}

	for _, address := range addresses {
		lowerAddr := strings.ToLower(address)

		participant := models.AddressParticipant{
			Address: address,
			Type:    addressTypes[lowerAddr],
		}

		// Set role and category from AI analysis
		if roles, exists := roleData[lowerAddr]; exists {
			participant.Role = roles["role"]
			participant.Category = roles["category"]
		} else {
			// Fallback roles
			participant.Role = "Unknown"
			participant.Category = "unknown"
		}

		// Add ENS name if available
		if ensName, exists := ensNames[lowerAddr]; exists {
			participant.ENSName = ensName
		}

		// Add token metadata if this is a token contract
		if metadata, exists := tokenMetadata[lowerAddr]; exists {
			participant.Name = fmt.Sprintf("%s (%s)", metadata.Name, metadata.Symbol)
			participant.Description = fmt.Sprintf("%s token contract", metadata.Type)
		}

		// Generate explorer link
		if network.Explorer != "" {
			participant.Link = fmt.Sprintf("%s/address/%s", network.Explorer, address)
		}

		participants = append(participants, participant)
	}

	return participants
}

// GetPromptContext provides context for other tools to use
func (a *AddressRoleResolver) GetPromptContext(ctx context.Context, baggage map[string]interface{}) string {
	participants, ok := baggage["address_participants"].([]models.AddressParticipant)
	if !ok || len(participants) == 0 {
		return ""
	}

	var contextParts []string
	contextParts = append(contextParts, "TRANSACTION PARTICIPANTS:")

	// Group by category for better organization
	categories := make(map[string][]models.AddressParticipant)
	for _, p := range participants {
		categories[p.Category] = append(categories[p.Category], p)
	}

	for category, categoryParticipants := range categories {
		contextParts = append(contextParts, fmt.Sprintf("\n%s addresses:", strings.Title(category)))
		for _, p := range categoryParticipants {
			addressInfo := fmt.Sprintf("- %s: %s [%s]", p.Address, p.Role, p.Type)
			if p.ENSName != "" {
				addressInfo += fmt.Sprintf(" (%s)", p.ENSName)
			}
			if p.Name != "" {
				addressInfo += fmt.Sprintf(" - %s", p.Name)
			}
			contextParts = append(contextParts, addressInfo)
		}
	}

	return strings.Join(contextParts, "\n")
}

// GetRagContext provides RAG context for address role information  
// Address roles are small structured data, so we keep them in prompt context instead of RAG
func (a *AddressRoleResolver) GetRagContext(ctx context.Context, baggage map[string]interface{}) *RagContext {
	ragContext := NewRagContext()
	// Address roles are small and structured, better kept in direct prompt context
	return ragContext
}
