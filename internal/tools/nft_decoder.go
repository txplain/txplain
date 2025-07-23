package tools

import (
	"context"
	"fmt" // Added for os.Getenv
	"strconv"
	"strings"

	"github.com/txplain/txplain/internal/models"
	"github.com/txplain/txplain/internal/rpc"
)

// NFTDecoder extracts and enriches NFT transfers from events
type NFTDecoder struct {
	rpcClient *rpc.Client
	verbose   bool
}

// NFTTransfer represents an NFT transfer with metadata
type NFTTransfer struct {
	Type           string `json:"type"`            // ERC721, ERC1155
	Contract       string `json:"contract"`        // NFT contract address
	From           string `json:"from"`            // Sender address
	To             string `json:"to"`              // Receiver address
	TokenID        string `json:"token_id"`        // Token ID
	Amount         string `json:"amount"`          // Amount (for ERC1155, always "1" for ERC721)
	Name           string `json:"name"`            // Contract name
	Symbol         string `json:"symbol"`          // Contract symbol
	TokenURI       string `json:"token_uri"`       // Token metadata URI (if available)
	CollectionName string `json:"collection_name"` // Human-friendly collection name
}

// NewNFTDecoder creates a new NFT decoder
func NewNFTDecoder(verbose bool, rpcClient *rpc.Client) *NFTDecoder {
	return &NFTDecoder{
		rpcClient: rpcClient,
		verbose:   verbose,
	}
}

// Name returns the processor name
func (n *NFTDecoder) Name() string {
	return "nft_decoder"
}

// Description returns the processor description
func (n *NFTDecoder) Description() string {
	return "Decodes and enriches ERC721 and ERC1155 NFT transfers from events"
}

// Dependencies returns the tools this processor depends on
func (n *NFTDecoder) Dependencies() []string {
	return []string{"log_decoder"} // Needs decoded events
}

// Process extracts NFT transfers from events and adds them to baggage
func (n *NFTDecoder) Process(ctx context.Context, baggage map[string]interface{}) error {
	if n.verbose {
		fmt.Println("\n" + strings.Repeat("🖼️", 60))
		fmt.Println("🔍 NFT DECODER: Starting NFT transfer extraction")
		fmt.Printf("🔧 RPC client available: %t\n", n.rpcClient != nil)
		fmt.Println(strings.Repeat("🖼️", 60))
	}

	// Get events from baggage
	events, ok := baggage["events"].([]models.Event)
	if !ok || len(events) == 0 {
		if n.verbose {
			fmt.Println("⚠️  No events found, skipping NFT extraction")
			fmt.Println(strings.Repeat("🖼️", 60) + "\n")
		}
		return nil // No events to process
	}

	if n.verbose {
		fmt.Printf("📊 Processing %d events for NFT transfers\n", len(events))
	}

	// Extract NFT transfers
	nftTransfers := n.extractNFTTransfers(ctx, events)

	if len(nftTransfers) == 0 {
		if n.verbose {
			fmt.Println("⚠️  No NFT transfers found in events")
			fmt.Println(strings.Repeat("🖼️", 60) + "\n")
		}
		return nil // No NFT transfers found
	}

	if n.verbose {
		fmt.Printf("🎨 Extracted %d NFT transfers\n", len(nftTransfers))
	}

	// Enrich with contract metadata if RPC client is available
	if n.rpcClient != nil {
		if n.verbose {
			fmt.Println("🔄 Enriching NFT transfers with contract metadata...")
		}

		successCount := 0
		for i, transfer := range nftTransfers {
			if n.verbose {
				fmt.Printf("   [%d/%d] Enriching %s (Token ID: %s)...", i+1, len(nftTransfers), transfer.Contract[:10]+"...", transfer.TokenID)
			}

			if contractInfo, err := n.rpcClient.GetContractInfo(ctx, transfer.Contract); err == nil {
				nftTransfers[i].Name = contractInfo.Name
				nftTransfers[i].Symbol = contractInfo.Symbol

				// Try to create a human-friendly collection name
				if contractInfo.Name != "" {
					nftTransfers[i].CollectionName = contractInfo.Name
				} else if contractInfo.Symbol != "" {
					nftTransfers[i].CollectionName = contractInfo.Symbol
				} else {
					nftTransfers[i].CollectionName = "Unknown Collection"
				}

				successCount++
				if n.verbose {
					collectionName := nftTransfers[i].CollectionName
					if collectionName == "Unknown Collection" {
						fmt.Printf(" ⚪ No metadata\n")
					} else {
						fmt.Printf(" ✅ %s\n", collectionName)
					}
				}
			} else {
				if n.verbose {
					fmt.Printf(" ❌ Failed: %v\n", err)
				}
			}
		}

		if n.verbose {
			fmt.Printf("✅ Successfully enriched %d/%d NFT transfers\n", successCount, len(nftTransfers))
		}
	} else if n.verbose {
		fmt.Println("⚠️  No RPC client available, skipping metadata enrichment")
	}

	if n.verbose {
		// Show summary of NFT transfers
		if len(nftTransfers) > 0 {
			fmt.Println("\n📋 NFT TRANSFERS SUMMARY:")
			for i, transfer := range nftTransfers {
				transferDisplay := fmt.Sprintf("   %d. %s Token ID %s", i+1, transfer.Type, transfer.TokenID)
				if transfer.CollectionName != "" && transfer.CollectionName != "Unknown Collection" {
					transferDisplay += fmt.Sprintf(" (%s)", transfer.CollectionName)
				}
				transferDisplay += fmt.Sprintf(" - %s → %s", transfer.From[:10]+"...", transfer.To[:10]+"...")
				fmt.Println(transferDisplay)
			}
		}

		fmt.Println("\n" + strings.Repeat("🖼️", 60))
		fmt.Println("✅ NFT DECODER: Completed successfully")
		fmt.Println(strings.Repeat("🖼️", 60) + "\n")
	}

	// Add NFT transfers to baggage
	baggage["nft_transfers"] = nftTransfers

	return nil
}

// extractNFTTransfers extracts NFT transfers from events
func (n *NFTDecoder) extractNFTTransfers(ctx context.Context, events []models.Event) []NFTTransfer {
	var transfers []NFTTransfer

	for _, event := range events {
		// Try to parse as different NFT transfer types based on parameters, not hardcoded names
		if transfer := n.parseERC721Transfer(event); transfer != nil {
			transfers = append(transfers, *transfer)
		} else if transfer := n.parseERC1155TransferSingle(event); transfer != nil {
			transfers = append(transfers, *transfer)
		} else if batchTransfers := n.parseERC1155TransferBatch(event); len(batchTransfers) > 0 {
			transfers = append(transfers, batchTransfers...)
		}
	}

	return transfers
}

// parseERC721Transfer parses an ERC721 Transfer event
func (n *NFTDecoder) parseERC721Transfer(event models.Event) *NFTTransfer {
	if event.Parameters == nil {
		return nil
	}

	// ENHANCED VALIDATION: Check if this looks like an ERC721 transfer based on parameters
	if !n.looksLikeERC721Transfer(event) {
		return nil
	}

	// Check if this is an NFT transfer (has tokenId parameter)
	tokenID, hasTokenID := event.Parameters["tokenId"].(string)
	if !hasTokenID {
		return nil // This is likely an ERC20 transfer, not NFT
	}

	from, _ := event.Parameters["from"].(string)
	to, _ := event.Parameters["to"].(string)

	// Additional validation: ensure we have proper from/to addresses
	if from == "" || to == "" {
		return nil
	}

	// Clean addresses (remove padding)
	from = n.cleanAddress(from)
	to = n.cleanAddress(to)

	// Final validation: ensure cleaned addresses are valid
	if !n.isValidAddress(from) || !n.isValidAddress(to) {
		return nil
	}

	return &NFTTransfer{
		Type:     "ERC721",
		Contract: event.Contract,
		From:     from,
		To:       to,
		TokenID:  n.cleanTokenID(tokenID),
		Amount:   "1", // ERC721 always transfers 1 NFT
	}
}

// parseERC1155TransferSingle parses an ERC1155 TransferSingle event
func (n *NFTDecoder) parseERC1155TransferSingle(event models.Event) *NFTTransfer {
	if event.Parameters == nil {
		return nil
	}

	// ENHANCED VALIDATION: Check if this looks like an ERC1155 single transfer
	if !n.looksLikeERC1155Single(event) {
		return nil
	}

	from, _ := event.Parameters["from"].(string)
	to, _ := event.Parameters["to"].(string)
	tokenID, _ := event.Parameters["id"].(string)
	amount, _ := event.Parameters["value"].(string)

	// Additional validation: ensure we have all required parameters
	if from == "" || to == "" || tokenID == "" || amount == "" {
		return nil
	}

	// Clean addresses and token data
	from = n.cleanAddress(from)
	to = n.cleanAddress(to)
	tokenID = n.cleanTokenID(tokenID)
	amount = n.formatAmount(amount)

	// Final validation: ensure cleaned addresses are valid
	if !n.isValidAddress(from) || !n.isValidAddress(to) {
		return nil
	}

	return &NFTTransfer{
		Type:     "ERC1155",
		Contract: event.Contract,
		From:     from,
		To:       to,
		TokenID:  tokenID,
		Amount:   amount,
	}
}

// parseERC1155TransferBatch parses an ERC1155 TransferBatch event
func (n *NFTDecoder) parseERC1155TransferBatch(event models.Event) []NFTTransfer {
	if event.Parameters == nil {
		return nil
	}

	// ENHANCED VALIDATION: Check if this looks like an ERC1155 batch transfer
	if !n.looksLikeERC1155Batch(event) {
		return nil
	}

	from, _ := event.Parameters["from"].(string)
	to, _ := event.Parameters["to"].(string)

	// Additional validation: ensure we have required parameters
	if from == "" || to == "" {
		return nil
	}

	// Clean addresses
	from = n.cleanAddress(from)
	to = n.cleanAddress(to)

	// Final validation: ensure cleaned addresses are valid
	if !n.isValidAddress(from) || !n.isValidAddress(to) {
		return nil
	}

	// For batch transfers, we'd need to parse arrays of IDs and values
	// This is complex and would require proper ABI decoding
	// For now, create a single transfer representing the batch
	return []NFTTransfer{
		{
			Type:     "ERC1155",
			Contract: event.Contract,
			From:     from,
			To:       to,
			TokenID:  "batch", // Placeholder for batch transfers
			Amount:   "multiple",
		},
	}
}

// looksLikeERC721Transfer detects ERC721 transfer pattern based on parameters
func (n *NFTDecoder) looksLikeERC721Transfer(event models.Event) bool {
	if event.Parameters == nil {
		return false
	}

	// ERC721 Transfer pattern: from, to, tokenId parameters
	_, hasFrom := event.Parameters["from"]
	_, hasTo := event.Parameters["to"]
	_, hasTokenId := event.Parameters["tokenId"]

	// Must have all three parameters and NOT have ERC1155-specific parameters
	_, hasOperator := event.Parameters["operator"]
	_, hasId := event.Parameters["id"] // ERC1155 uses 'id' instead of 'tokenId'

	return hasFrom && hasTo && hasTokenId && !hasOperator && !hasId
}

// looksLikeERC1155Single detects ERC1155 TransferSingle pattern based on parameters
func (n *NFTDecoder) looksLikeERC1155Single(event models.Event) bool {
	if event.Parameters == nil {
		return false
	}

	// ERC1155 TransferSingle pattern: operator, from, to, id, value parameters
	_, hasOperator := event.Parameters["operator"]
	_, hasFrom := event.Parameters["from"]
	_, hasTo := event.Parameters["to"]
	_, hasId := event.Parameters["id"]
	_, hasValue := event.Parameters["value"]

	return hasOperator && hasFrom && hasTo && hasId && hasValue
}

// looksLikeERC1155Batch detects ERC1155 TransferBatch pattern based on parameters
func (n *NFTDecoder) looksLikeERC1155Batch(event models.Event) bool {
	if event.Parameters == nil {
		return false
	}

	// ERC1155 TransferBatch pattern: operator, from, to parameters
	// (ids and values are in data field as arrays, harder to detect)
	_, hasOperator := event.Parameters["operator"]
	_, hasFrom := event.Parameters["from"]
	_, hasTo := event.Parameters["to"]

	// Check for batch indicators
	_, hasBatchTransfer := event.Parameters["batch_transfer"]
	_, hasRawData := event.Parameters["raw_data"]

	return hasOperator && hasFrom && hasTo && (hasBatchTransfer || hasRawData)
}

// isValidAddress validates that a string is a valid Ethereum address
func (n *NFTDecoder) isValidAddress(address string) bool {
	if address == "" {
		return false
	}

	// Must start with 0x and be exactly 42 characters (0x + 40 hex chars)
	if !strings.HasPrefix(address, "0x") || len(address) != 42 {
		return false
	}

	// Check if the remaining characters are valid hex
	for _, char := range address[2:] {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}

	return true
}

// cleanAddress removes leading zeros from address padding
func (n *NFTDecoder) cleanAddress(address string) string {
	if address == "" {
		return ""
	}

	// If it's a padded address (64 chars after 0x), extract the last 40 chars
	if strings.HasPrefix(address, "0x") && len(address) == 66 {
		return "0x" + address[26:] // Take last 40 characters
	}

	return address
}

// cleanTokenID removes leading zeros from token ID
func (n *NFTDecoder) cleanTokenID(tokenID string) string {
	if tokenID == "" {
		return "0"
	}

	// Convert hex to decimal for cleaner display
	if strings.HasPrefix(tokenID, "0x") {
		if id, err := strconv.ParseUint(tokenID[2:], 16, 64); err == nil {
			return fmt.Sprintf("%d", id)
		}
	}

	return tokenID
}

// formatAmount formats transfer amounts for display
func (n *NFTDecoder) formatAmount(amount string) string {
	if amount == "" {
		return "1"
	}

	// Convert hex to decimal for cleaner display
	if strings.HasPrefix(amount, "0x") {
		if amt, err := strconv.ParseUint(amount[2:], 16, 64); err == nil {
			return fmt.Sprintf("%d", amt)
		}
	}

	return amount
}

// GetPromptContext provides NFT context for LLM prompts
func (n *NFTDecoder) GetPromptContext(ctx context.Context, baggage map[string]interface{}) string {
	// Get NFT transfers from baggage
	nftTransfers, ok := baggage["nft_transfers"].([]NFTTransfer)
	if !ok || len(nftTransfers) == 0 {
		return ""
	}

	// Build context string
	var contextParts []string
	contextParts = append(contextParts, "### NFT Transfers:")

	// Group transfers by type for better organization
	erc721Transfers := []NFTTransfer{}
	erc1155Transfers := []NFTTransfer{}

	for _, transfer := range nftTransfers {
		if transfer.Type == "ERC721" {
			erc721Transfers = append(erc721Transfers, transfer)
		} else if transfer.Type == "ERC1155" {
			erc1155Transfers = append(erc1155Transfers, transfer)
		}
	}

	// Add ERC721 transfers
	if len(erc721Transfers) > 0 {
		contextParts = append(contextParts, fmt.Sprintf("\n#### ERC-721 Tokens Transferred: %d", len(erc721Transfers)))
		for i, transfer := range erc721Transfers {
			transferInfo := fmt.Sprintf("\nNFT Transfer #%d:", i+1)
			transferInfo += fmt.Sprintf("\n- Collection: %s", n.getDisplayName(transfer))
			transferInfo += fmt.Sprintf("\n- Token ID: %s", transfer.TokenID)
			transferInfo += fmt.Sprintf("\n- From: %s", transfer.From)
			transferInfo += fmt.Sprintf("\n- To: %s (NFT RECIPIENT)", transfer.To)
			transferInfo += fmt.Sprintf("\n- Contract: %s", transfer.Contract)
			contextParts = append(contextParts, transferInfo)
		}
	}

	// Add ERC1155 transfers with enhanced amount formatting
	if len(erc1155Transfers) > 0 {
		contextParts = append(contextParts, fmt.Sprintf("\n#### ERC-1155 Tokens Transferred: %d", len(erc1155Transfers)))
		for i, transfer := range erc1155Transfers {
			transferInfo := fmt.Sprintf("\nERC-1155 Transfer #%d:", i+1)
			transferInfo += fmt.Sprintf("\n- Collection: %s", n.getDisplayName(transfer))
			transferInfo += fmt.Sprintf("\n- Token ID: %s", transfer.TokenID)

			// Enhanced amount formatting
			amount := transfer.Amount
			if amt, err := strconv.Atoi(amount); err == nil {
				if amt >= 1000 {
					transferInfo += fmt.Sprintf("\n- Amount: %s (%s tokens)", n.formatLargeNumber(amt), amount)
				} else {
					transferInfo += fmt.Sprintf("\n- Amount: %s tokens", amount)
				}
			} else {
				transferInfo += fmt.Sprintf("\n- Amount: %s", amount)
			}

			transferInfo += fmt.Sprintf("\n- From: %s", transfer.From)
			transferInfo += fmt.Sprintf("\n- To: %s (NFT RECIPIENT)", transfer.To)
			transferInfo += fmt.Sprintf("\n- Contract: %s", transfer.Contract)
			contextParts = append(contextParts, transferInfo)
		}
	}

	// Add detailed recipient summary for final explanation
	if len(nftTransfers) > 0 {
		contextParts = append(contextParts, "\n\n#### Recipient Summary for Final Explanation:")

		// Group by token ID and collection for cleaner summary
		recipientSummary := n.buildRecipientSummary(nftTransfers)
		for _, summary := range recipientSummary {
			contextParts = append(contextParts, summary)
		}
	}

	// Add classification hint for LLM
	var classificationHints []string

	// Check if NFTs are being minted (from zero address)
	mintCount := 0
	var recipients []string
	for _, transfer := range nftTransfers {
		if transfer.From == "0x0000000000000000000000000000000000000000" {
			mintCount++
			recipients = append(recipients, transfer.To)
		}
	}

	if mintCount > 0 {
		classificationHints = append(classificationHints, fmt.Sprintf("- %d NFT(s) were MINTED (from zero address) to %d recipients", mintCount, len(n.uniqueAddresses(recipients))))
		classificationHints = append(classificationHints, "- This is a MINTING transaction, not a purchase/transfer")
		classificationHints = append(classificationHints, "- CRITICAL: Show specific recipient addresses and amounts in final explanation")

		// List unique recipients
		uniqueRecipients := n.uniqueAddresses(recipients)
		if len(uniqueRecipients) > 1 {
			classificationHints = append(classificationHints, fmt.Sprintf("- NFTs minted to multiple recipients: %v", uniqueRecipients))
		}
	}

	// Check for large amounts suggesting fungible-style usage
	for _, transfer := range erc1155Transfers {
		if amt, err := strconv.Atoi(transfer.Amount); err == nil && amt >= 100 {
			classificationHints = append(classificationHints, fmt.Sprintf("- Large amount (%d) suggests fungible token usage (not traditional NFT)", amt))
		}
	}

	if len(classificationHints) > 0 {
		contextParts = append(contextParts, "\n\n#### Transaction Classification Hints:")
		for _, hint := range classificationHints {
			contextParts = append(contextParts, "\n"+hint)
		}
	}

	return strings.Join(contextParts, "")
}

// buildRecipientSummary creates a detailed summary of recipients and amounts for the LLM
func (n *NFTDecoder) buildRecipientSummary(transfers []NFTTransfer) []string {
	var summaries []string

	// Group transfers by collection and token ID
	groups := make(map[string][]NFTTransfer)
	for _, transfer := range transfers {
		key := fmt.Sprintf("%s_ID_%s", n.getDisplayName(transfer), transfer.TokenID)
		groups[key] = append(groups[key], transfer)
	}

	for groupKey, groupTransfers := range groups {
		if len(groupTransfers) == 1 {
			transfer := groupTransfers[0]
			summary := fmt.Sprintf("\n- %s amount %s → %s",
				groupKey, transfer.Amount, transfer.To)
			summaries = append(summaries, summary)
		} else {
			// Multiple transfers of the same token ID
			summary := fmt.Sprintf("\n- %s distributed:", groupKey)
			for _, transfer := range groupTransfers {
				summary += fmt.Sprintf("\n  • %s tokens → %s", transfer.Amount, transfer.To)
			}
			summaries = append(summaries, summary)
		}
	}

	// Add total summary
	if len(transfers) > 1 {
		totalRecipients := len(n.uniqueAddresses(n.extractRecipients(transfers)))
		summaries = append(summaries, fmt.Sprintf("\n- Total: %d transfers to %d unique recipients", len(transfers), totalRecipients))
	}

	return summaries
}

// extractRecipients extracts all recipient addresses from transfers
func (n *NFTDecoder) extractRecipients(transfers []NFTTransfer) []string {
	var recipients []string
	for _, transfer := range transfers {
		recipients = append(recipients, transfer.To)
	}
	return recipients
}

// getDisplayName returns the best display name for an NFT transfer
func (n *NFTDecoder) getDisplayName(transfer NFTTransfer) string {
	if transfer.CollectionName != "" && transfer.CollectionName != "Unknown Collection" {
		return transfer.CollectionName
	}
	if transfer.Name != "" {
		return transfer.Name
	}
	if transfer.Symbol != "" {
		return transfer.Symbol
	}
	return "Unknown NFT"
}

// formatLargeNumber formats large numbers with commas for readability
func (n *NFTDecoder) formatLargeNumber(num int) string {
	str := fmt.Sprintf("%d", num)
	if len(str) <= 3 {
		return str
	}

	// Add commas for thousands separator
	var result strings.Builder
	for i, char := range str {
		if i > 0 && (len(str)-i)%3 == 0 {
			result.WriteRune(',')
		}
		result.WriteRune(char)
	}
	return result.String()
}

// uniqueAddresses returns unique addresses from a slice
func (n *NFTDecoder) uniqueAddresses(addresses []string) []string {
	seen := make(map[string]bool)
	var unique []string

	for _, addr := range addresses {
		if !seen[addr] {
			seen[addr] = true
			unique = append(unique, addr)
		}
	}

	return unique
}

// GetNFTTransfers is a helper function to get NFT transfers from baggage
func GetNFTTransfers(baggage map[string]interface{}) ([]NFTTransfer, bool) {
	if transfers, ok := baggage["nft_transfers"].([]NFTTransfer); ok {
		return transfers, true
	}
	return nil, false
}

// GetRagContext provides RAG context for NFT information
func (n *NFTDecoder) GetRagContext(ctx context.Context, baggage map[string]interface{}) *RagContext {
	ragContext := NewRagContext()

	nftTransfers, ok := baggage["nft_transfers"].([]NFTTransfer)
	if !ok || len(nftTransfers) == 0 {
		return ragContext
	}

	// Add NFT collection information to RAG context for searchability
	seen := make(map[string]bool)
	for _, transfer := range nftTransfers {
		if transfer.Name != "" && !seen[transfer.Contract] {
			seen[transfer.Contract] = true

			ragContext.AddItem(RagContextItem{
				ID:      fmt.Sprintf("nft_%s", transfer.Contract),
				Type:    "nft",
				Title:   fmt.Sprintf("%s NFT Collection", transfer.Name),
				Content: fmt.Sprintf("NFT collection %s (%s) at contract %s supports %s standard", transfer.Name, transfer.Symbol, transfer.Contract, transfer.Type),
				Metadata: map[string]interface{}{
					"contract":        transfer.Contract,
					"name":            transfer.Name,
					"symbol":          transfer.Symbol,
					"type":            transfer.Type,
					"collection_name": transfer.CollectionName,
				},
				Keywords:  []string{transfer.Name, transfer.Symbol, transfer.CollectionName, "nft", transfer.Type},
				Relevance: 0.7,
			})
		}
	}

	return ragContext
}
