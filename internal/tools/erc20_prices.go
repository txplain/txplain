package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/txplain/txplain/internal/models"
)

// ERC20PriceLookup fetches token prices using PriceService
type ERC20PriceLookup struct {
	priceService PriceService
	verbose      bool
	cache        Cache // Cache for price data
}

// Removed DEXPriceData - not needed for simplified Coingecko approach

// DEXListingInfo represents DEX listing metadata information
type DEXListingInfo struct {
	ID              int                    `json:"id"`
	Name            string                 `json:"name"`
	Symbol          string                 `json:"symbol"`
	Slug            string                 `json:"slug"`
	Logo            string                 `json:"logo"`
	Description     string                 `json:"description"`
	DateLaunched    string                 `json:"date_launched"`
	Notice          string                 `json:"notice"`
	Status          string                 `json:"status"`
	Category        string                 `json:"category"`
	URLs            map[string]interface{} `json:"urls"`
	ContractAddress string                 `json:"contract_address,omitempty"`
	Platform        string                 `json:"platform,omitempty"`
	NetworkSlug     string                 `json:"network_slug,omitempty"`
}

// TokenPrice represents simplified price information for a token
type TokenPrice struct {
	Symbol      string    `json:"symbol"`
	Name        string    `json:"name,omitempty"`
	Contract    string    `json:"contract_address,omitempty"`
	Price       float64   `json:"price"`
	LastUpdated time.Time `json:"last_updated"`
	PriceSource string    `json:"price_source,omitempty"`
	// Fields for transfer calculations
	TransferAmounts map[string]float64 `json:"transfer_amounts,omitempty"` // transfer_id -> amount in tokens
	TransferValues  map[string]float64 `json:"transfer_values,omitempty"`  // transfer_id -> USD value
}

// NewERC20PriceLookup creates a new ERC20 price lookup tool
func NewERC20PriceLookup(priceService PriceService, cache Cache, verbose bool) *ERC20PriceLookup {
	if priceService == nil {
		panic("PriceService is required for ERC20PriceLookup")
	}

	return &ERC20PriceLookup{
		priceService: priceService,
		verbose:      verbose,
		cache:        cache,
	}
}

// Name returns the tool name
func (t *ERC20PriceLookup) Name() string {
	return "erc20_price_lookup"
}

// Description returns the tool description
func (t *ERC20PriceLookup) Description() string {
	return "Looks up ERC20 token prices using Coingecko API with DEX data for ERC20 tokens and native token pricing. Supports lookup by contract address."
}

// Dependencies returns the tools this processor depends on
func (t *ERC20PriceLookup) Dependencies() []string {
	return []string{"token_metadata_enricher", "amounts_finder"} // Needs token metadata for optimal lookups and amounts for native token pricing
}

// Process adds token price information to baggage
func (t *ERC20PriceLookup) Process(ctx context.Context, baggage map[string]interface{}) error {
	if t.verbose {
		fmt.Println("\n" + strings.Repeat("💰", 60))
		fmt.Println("🔍 ERC20 PRICE LOOKUP: Starting token price fetching")
		fmt.Printf("🔑 Price Service available: %t\n", t.priceService.IsAvailable())
		fmt.Println(strings.Repeat("💰", 60))
	}

	if !t.priceService.IsAvailable() {
		if t.verbose {
			fmt.Println("⚠️  No price service available, skipping price lookup")
			fmt.Println(strings.Repeat("💰", 60) + "\n")
		}
		return nil // No price service, skip price lookup
	}

	// Get network ID from baggage for DEX pricing
	networkID := int64(0) // No default - require explicit network
	if rawData, ok := baggage["raw_data"].(map[string]interface{}); ok {
		if nid, ok := rawData["network_id"].(float64); ok {
			networkID = int64(nid)
		}
	}

	// Skip if no network ID provided
	if networkID == 0 {
		if t.verbose {
			fmt.Println("⚠️  No network ID provided, skipping price lookup")
			fmt.Println(strings.Repeat("💰", 60) + "\n")
		}
		return nil
	}

	if t.verbose {
		fmt.Printf("🌐 Network ID: %d\n", networkID)
	}

	// Check if we need native token price for gas fees BEFORE checking token metadata
	needsNativeTokenPrice := t.checkForNativeTokenNeeds(baggage, networkID)

	// Get token metadata from baggage
	tokenMetadata, ok := baggage["token_metadata"].(map[string]*TokenMetadata)
	if (!ok || len(tokenMetadata) == 0) && !needsNativeTokenPrice {
		if t.verbose {
			fmt.Println("⚠️  No token metadata found and no native token price needed, skipping price lookup")
			fmt.Println(strings.Repeat("💰", 60) + "\n")
		}
		return nil // No token metadata and no native token needed, nothing to price
	}

	// Get progress tracker from baggage if available
	progressTracker, hasProgress := baggage["progress_tracker"].(*models.ProgressTracker)

	if t.verbose {
		if tokenMetadata != nil && len(tokenMetadata) > 0 {
			fmt.Printf("📊 Found %d tokens to price\n", len(tokenMetadata))

			// Show tokens to be priced
			erc20Count := 0
			for _, metadata := range tokenMetadata {
				if metadata.Type == "ERC20" || (metadata.Type == "Contract" && metadata.Decimals > 0) {
					erc20Count++
				}
			}
			fmt.Printf("💹 %d tokens eligible for pricing\n", erc20Count)
		} else {
			fmt.Printf("📊 No ERC20 tokens found, but native token price needed\n")
		}
		fmt.Println("🔄 Looking up token prices...")
	}

	// Look up prices for each token
	tokenPrices := make(map[string]*TokenPrice)
	successCount := 0
	totalToProcess := 0

	// Count total tokens to process for progress updates
	if needsNativeTokenPrice {
		totalToProcess++
	}
	if tokenMetadata != nil {
		for _, metadata := range tokenMetadata {
			if metadata.Type == "ERC20" || (metadata.Type == "Contract" && metadata.Decimals > 0) {
				totalToProcess++
			}
		}
	}

	currentIndex := 0

	// First, check if we need to fetch native token price for gas fees
	if needsNativeTokenPrice {
		currentIndex++
		nativeSymbol := t.priceService.GetNativeTokenSymbol(networkID)
		if nativeSymbol != "" {
			// Send progress update for native token pricing
			if hasProgress {
				progress := fmt.Sprintf("Fetching native token price (%d/%d): %s", currentIndex, totalToProcess, nativeSymbol)
				progressTracker.UpdateComponent("erc20_price_lookup", models.ComponentGroupEnrichment, "Fetching Token Prices", models.ComponentStatusRunning, progress)
			}

			if t.verbose {
				fmt.Printf("   Native %s (gas fees)...", nativeSymbol)
			}

			// Look up native token price from price service
			if price, err := t.priceService.GetNativeTokenPrice(ctx, networkID); err == nil {
				// Store native token price using "native" as key for easy lookup
				tokenPrices["native"] = &TokenPrice{
					Symbol:      nativeSymbol,
					Price:       price,
					PriceSource: "price_service",
				}
				successCount++

				if t.verbose {
					fmt.Printf(" ✅ $%.6f\n", price)
				}
			} else if t.verbose {
				fmt.Printf(" ❌ Failed: %v\n", err)
			}
		}
	}

	// Then process regular ERC20 tokens
	if tokenMetadata != nil {
		for address, metadata := range tokenMetadata {
			if metadata.Type == "ERC20" {
				currentIndex++

				// Send progress update for each ERC20 token
				if hasProgress {
					progress := fmt.Sprintf("Fetching ERC20 price (%d/%d): %s", currentIndex, totalToProcess, metadata.Symbol)
					progressTracker.UpdateComponent("erc20_price_lookup", models.ComponentGroupEnrichment, "Fetching Token Prices", models.ComponentStatusRunning, progress)
				}

				if t.verbose {
					fmt.Printf("   ERC20 %s (%s)...", metadata.Symbol, address[:10]+"...")
				}

				priceInput := map[string]interface{}{
					"contract_address": address,
					"network_id":       float64(networkID), // Convert to float64 to match Run method expectation
				}
				if metadata.Symbol != "" {
					priceInput["symbol"] = metadata.Symbol
				}

				result, err := t.Run(ctx, priceInput)
				if err == nil {
					if tokenData, ok := result["token"].(*TokenPrice); ok {
						tokenData.Contract = address // Ensure contract address is set
						tokenPrices[address] = tokenData
						successCount++

						if t.verbose {
							fmt.Printf(" ✅ $%.6f\n", tokenData.Price)
						}
					} else if t.verbose {
						fmt.Printf(" ❌ Invalid response format\n")
					}
				} else if t.verbose {
					fmt.Printf(" ❌ %v\n", err)
				}
			} else if metadata.Type == "Contract" && metadata.Decimals > 0 {
				currentIndex++

				// Send progress update for contract tokens
				if hasProgress {
					progress := fmt.Sprintf("Fetching contract price (%d/%d): %s", currentIndex, totalToProcess, address[:10]+"...")
					progressTracker.UpdateComponent("erc20_price_lookup", models.ComponentGroupEnrichment, "Fetching Token Prices", models.ComponentStatusRunning, progress)
				}

				if t.verbose {
					fmt.Printf("   Contract %s (has decimals)...", address[:10]+"...")
				}

				// Try DEX pricing for contracts that might be tokens (have decimals but no name/symbol)
				priceInput := map[string]interface{}{
					"contract_address": address,
					"network_id":       float64(networkID), // Convert to float64 to match Run method expectation
				}

				result, err := t.Run(ctx, priceInput)
				if err == nil {
					if tokenData, ok := result["token"].(*TokenPrice); ok {
						tokenData.Contract = address // Ensure contract address is set
						tokenPrices[address] = tokenData
						successCount++

						if t.verbose {
							fmt.Printf(" ✅ $%.6f (DEX)\n", tokenData.Price)
						}
					} else if t.verbose {
						fmt.Printf(" ❌ Invalid response format\n")
					}
				} else if t.verbose {
					fmt.Printf(" ❌ %v\n", err)
				}
			}
		}
	} // End of tokenMetadata != nil check

	// Send final progress update with results summary
	if hasProgress {
		if successCount > 0 {
			progress := fmt.Sprintf("Completed: Found prices for %d out of %d tokens", successCount, totalToProcess)
			progressTracker.UpdateComponent("erc20_price_lookup", models.ComponentGroupEnrichment, "Fetching Token Prices", models.ComponentStatusFinished, progress)
		} else {
			progress := fmt.Sprintf("Completed: No prices found for %d tokens", totalToProcess)
			progressTracker.UpdateComponent("erc20_price_lookup", models.ComponentGroupEnrichment, "Fetching Token Prices", models.ComponentStatusFinished, progress)
		}
	}

	if t.verbose {
		fmt.Printf("✅ Successfully fetched prices for %d/%d eligible tokens\n", successCount, len(tokenMetadata))
	}

	// Calculate USD values for transfers if available
	t.calculateTransferValues(baggage, tokenPrices, tokenMetadata)

	if t.verbose {
		fmt.Println("\n" + strings.Repeat("💰", 60))
		fmt.Println("✅ ERC20 PRICE LOOKUP: Completed successfully")
		fmt.Println(strings.Repeat("💰", 60) + "\n")
	}

	// Add token prices to baggage
	baggage["token_prices"] = tokenPrices
	return nil
}

// checkForNativeTokenNeeds determines if native token price needs to be fetched for gas fees
func (t *ERC20PriceLookup) checkForNativeTokenNeeds(baggage map[string]interface{}, networkID int64) bool {
	// Check if AmountsFinder detected any native token amounts (like gas fees)
	detectedAmounts, ok := baggage["detected_amounts"].([]DetectedAmount)
	if !ok {
		return false
	}

	// Look for any detected amounts with token_contract="native"
	for _, amount := range detectedAmounts {
		if amount.TokenContract == "native" {
			return true // Found native token usage, need to fetch price
		}
	}

	return false
}

// lookupTokenPrice implements the core token price lookup logic
func (t *ERC20PriceLookup) lookupTokenPrice(ctx context.Context, input map[string]interface{}) (*TokenPrice, error) {
	// Extract search parameters from input
	contractAddress, _ := input["contract_address"].(string)
	networkIDFloat, _ := input["network_id"].(float64)
	networkID := int64(networkIDFloat)

	if networkID == 0 {
		return nil, NewToolError("erc20_price_lookup", "network_id is required", "MISSING_NETWORK")
	}

	// For price service, we need the contract address
	if contractAddress == "" {
		return nil, NewToolError("erc20_price_lookup", "contract_address is required", "MISSING_CONTRACT")
	}

	// Use price service
	return t.getTokenPrice(ctx, contractAddress, networkID)
}

// calculateTransferValues calculates USD values for ERC20 transfers
func (t *ERC20PriceLookup) calculateTransferValues(baggage map[string]interface{}, tokenPrices map[string]*TokenPrice, tokenMetadata map[string]*TokenMetadata) {
	// Get transfers from baggage
	transfers, ok := baggage["transfers"]
	if !ok {
		return
	}

	for address, price := range tokenPrices {
		metadata := tokenMetadata[address]
		if metadata == nil {
			continue
		}

		price.TransferAmounts = make(map[string]float64)
		price.TransferValues = make(map[string]float64)

		// Handle transfers as TokenTransfer slice or interface slice
		var tokenTransfers []interface{}
		switch v := transfers.(type) {
		case []interface{}:
			tokenTransfers = v
		default:
			continue
		}

		// Find transfers for this token
		for i, transferData := range tokenTransfers {
			var transfer map[string]interface{}

			// Convert to map for easier access
			switch v := transferData.(type) {
			case map[string]interface{}:
				transfer = v
			default:
				// Try to convert struct to map using JSON marshaling/unmarshaling
				if data, err := json.Marshal(transferData); err == nil {
					var mapped map[string]interface{}
					if err := json.Unmarshal(data, &mapped); err == nil {
						transfer = mapped
					}
				}
				if transfer == nil {
					continue
				}
			}

			// Check if this transfer is for our token
			tokenContract, _ := transfer["contract"].(string)
			if strings.EqualFold(tokenContract, address) {
				transferID := fmt.Sprintf("transfer_%d", i)

				// Get transfer amount
				amountStr, ok := transfer["amount"].(string)
				if !ok || amountStr == "" {
					continue
				}

				// Convert amount to actual token units
				tokenAmount := t.convertAmountToTokens(amountStr, metadata.Decimals)
				if tokenAmount > 0 {
					usdValue := tokenAmount * price.Price
					price.TransferAmounts[transferID] = tokenAmount
					price.TransferValues[transferID] = usdValue
				}
			}
		}
	}
}

// convertAmountToTokens converts a raw amount (usually in wei-like units) to token units
func (t *ERC20PriceLookup) convertAmountToTokens(amountStr string, decimals int) float64 {
	// Handle hex strings
	if strings.HasPrefix(amountStr, "0x") {
		// Convert hex to decimal
		amountBig := new(big.Int)
		if _, ok := amountBig.SetString(amountStr[2:], 16); !ok {
			return 0
		}
		amountStr = amountBig.String()
	}

	// Parse the amount as big.Int
	amountBig := new(big.Int)
	if _, ok := amountBig.SetString(amountStr, 10); !ok {
		return 0
	}

	// Convert to big.Float and adjust for decimals to maintain precision
	amountFloat := new(big.Float).SetInt(amountBig)

	if decimals > 0 {
		// Create divisor (10^decimals) as big.Float for precise division
		divisor := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil))
		amountFloat.Quo(amountFloat, divisor)
	}

	// Convert to float64 only at the end
	result, _ := amountFloat.Float64()
	return result
}

// Run executes the price lookup with both CEX and DEX data
func (t *ERC20PriceLookup) Run(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
	// Extract search parameters from input
	contractAddress, _ := input["contract_address"].(string)
	networkIDFloat, _ := input["network_id"].(float64)
	networkID := int64(networkIDFloat)

	if networkID == 0 {
		return nil, NewToolError("erc20_price_lookup", "network_id is required", "MISSING_NETWORK")
	}

	// For price service, we need the contract address
	if contractAddress == "" {
		return nil, NewToolError("erc20_price_lookup", "contract_address is required", "MISSING_CONTRACT")
	}

	// Use price service
	tokenPrice, err := t.getTokenPrice(ctx, contractAddress, networkID)
	if err != nil {
		return nil, err
	}

	// Set contract address if not already set
	if contractAddress != "" {
		tokenPrice.Contract = contractAddress
	}

	return map[string]interface{}{
		"token": tokenPrice,
	}, nil
}

// getTokenPrice fetches price from the configured price service
func (t *ERC20PriceLookup) getTokenPrice(ctx context.Context, contractAddress string, networkID int64) (*TokenPrice, error) {
	result, err := t.priceService.GetTokenPrice(ctx, networkID, contractAddress)
	if err != nil {
		return nil, err
	}

	// Convert TokenPriceResult to TokenPrice for compatibility
	return &TokenPrice{
		Symbol:      result.Symbol,
		Price:       result.Price,
		Contract:    result.Contract,
		LastUpdated: result.LastUpdated,
		PriceSource: result.PriceSource,
	}, nil
}

// GetPromptContext provides price context for token transfers to include in LLM prompts
func (t *ERC20PriceLookup) GetPromptContext(ctx context.Context, baggage map[string]interface{}) string {
	// Get token metadata and prices from baggage
	tokenMetadata, hasMetadata := baggage["token_metadata"].(map[string]*TokenMetadata)
	tokenPrices, hasPrices := baggage["token_prices"].(map[string]*TokenPrice)

	if !hasMetadata || !hasPrices || len(tokenPrices) == 0 {
		return ""
	}

	// Build context string with both base prices and transfer values
	var contextParts []string
	for address, price := range tokenPrices {
		if metadata, exists := tokenMetadata[address]; exists {
			// Format base price
			priceStr := fmt.Sprintf("$%.2f", price.Price)
			if price.Price < 0.01 {
				priceStr = fmt.Sprintf("$%.6f", price.Price)
			}

			basePriceInfo := fmt.Sprintf("- %s (%s): %s USD per token", metadata.Name, metadata.Symbol, priceStr)

			// Add price source information
			if price.PriceSource != "" {
				basePriceInfo += fmt.Sprintf(" [%s]", price.PriceSource)
			}

			// Add transfer values if available
			if len(price.TransferValues) > 0 {
				var transferInfo []string
				for transferID, usdValue := range price.TransferValues {
					tokenAmount := price.TransferAmounts[transferID]
					transferInfo = append(transferInfo, fmt.Sprintf("  • Transfer: %.6f %s = $%.2f USD",
						tokenAmount, metadata.Symbol, usdValue))
				}
				if len(transferInfo) > 0 {
					basePriceInfo += "\n" + strings.Join(transferInfo, "\n")
				}
			}

			contextParts = append(contextParts, basePriceInfo)
		}
	}

	if len(contextParts) == 0 {
		return ""
	}

	return "Token Prices (CEX + DEX Data):\n" + strings.Join(contextParts, "\n")
}

// GetRagContext provides RAG context for price information (minimal for this tool)
func (t *ERC20PriceLookup) GetRagContext(ctx context.Context, baggage map[string]interface{}) *RagContext {
	ragContext := NewRagContext()
	// Price lookup provides current market data, not historical knowledge for RAG
	return ragContext
}
