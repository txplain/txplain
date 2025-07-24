package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/txplain/txplain/internal/models"
	"github.com/txplain/txplain/internal/rpc"
)

// TokenMetadataEnricher enriches ERC20 token addresses with metadata
type TokenMetadataEnricher struct {
	rpcClient *rpc.Client
	cmcClient *CoinMarketCapClient
	verbose   bool
	cache     Cache // Cache for metadata lookups
}

// TokenMetadata represents metadata for a token
type TokenMetadata struct {
	Address     string `json:"address"`
	Name        string `json:"name"`
	Symbol      string `json:"symbol"`
	Decimals    int    `json:"decimals"`
	Type        string `json:"type"` // ERC20, ERC721, etc.
	Logo        string `json:"logo,omitempty"`
	Description string `json:"description,omitempty"`
	Website     string `json:"website,omitempty"`
	Category    string `json:"category,omitempty"`
}

// CoinMarketCapInfoResponse represents the response from /v1/cryptocurrency/info
type CoinMarketCapInfoResponse struct {
	Status struct {
		Timestamp    string `json:"timestamp"`
		ErrorCode    int    `json:"error_code"`
		ErrorMessage string `json:"error_message"`
		Elapsed      int    `json:"elapsed"`
		CreditCount  int    `json:"credit_count"`
	} `json:"status"`
	Data map[string]struct {
		ID          int    `json:"id"`
		Name        string `json:"name"`
		Symbol      string `json:"symbol"`
		Category    string `json:"category"`
		Description string `json:"description"`
		Slug        string `json:"slug"`
		Logo        string `json:"logo"`
		URLs        struct {
			Website      []string `json:"website"`
			TechnicalDoc []string `json:"technical_doc"`
			Explorer     []string `json:"explorer"`
			SourceCode   []string `json:"source_code"`
			MessageBoard []string `json:"message_board"`
			Chat         []string `json:"chat"`
			Facebook     []string `json:"facebook"`
			Twitter      []string `json:"twitter"`
			Reddit       []string `json:"reddit"`
		} `json:"urls"`
		Platform struct {
			ID           int    `json:"id"`
			Name         string `json:"name"`
			Symbol       string `json:"symbol"`
			Slug         string `json:"slug"`
			TokenAddress string `json:"token_address"`
		} `json:"platform,omitempty"`
	} `json:"data"`
}

// NewTokenMetadataEnricher creates a new token metadata enricher with provided CMC client
func NewTokenMetadataEnricher(cache Cache, verbose bool, rpcClient *rpc.Client, cmcClient *CoinMarketCapClient) *TokenMetadataEnricher {
	return &TokenMetadataEnricher{
		rpcClient: rpcClient,
		cmcClient: cmcClient,
		verbose:   verbose,
		cache:     cache,
	}
}

// NewTokenMetadataEnricherWithCMC creates a new token metadata enricher with a provided CMC client
func NewTokenMetadataEnricherWithCMC(cache Cache, verbose bool, rpcClient *rpc.Client, cmcClient *CoinMarketCapClient) *TokenMetadataEnricher {
	return &TokenMetadataEnricher{
		rpcClient: rpcClient,
		cmcClient: cmcClient,
		verbose:   verbose,
		cache:     cache,
	}
}

// Name returns the processor name
func (t *TokenMetadataEnricher) Name() string {
	return "token_metadata_enricher"
}

// Description returns the processor description
func (t *TokenMetadataEnricher) Description() string {
	return "Enriches ERC20 token addresses with metadata (name, symbol, decimals)"
}

// Dependencies returns the tools this processor depends on
func (t *TokenMetadataEnricher) Dependencies() []string {
	return []string{"abi_resolver"} // Needs contract addresses from ABI resolver
}

// Process enriches the baggage with token metadata by calling individual contract methods
func (t *TokenMetadataEnricher) Process(ctx context.Context, baggage map[string]interface{}) error {
	if t.verbose {
		fmt.Println("\n" + strings.Repeat("🪙", 60))
		fmt.Println("🔍 TOKEN METADATA ENRICHER: Starting token metadata enrichment")
		fmt.Printf("🔑 CoinMarketCap API Key available: %t\n", t.cmcClient.IsAvailable())
		fmt.Println(strings.Repeat("🪙", 60))
	}

	// Get contract addresses discovered by ABI resolver
	contractAddresses, ok := baggage["contract_addresses"].([]string)
	if !ok || len(contractAddresses) == 0 {
		if t.verbose {
			fmt.Println("⚠️  No contract addresses found, skipping token metadata enrichment")
			fmt.Println(strings.Repeat("🪙", 60) + "\n")
		}
		return nil // No contracts to check
	}

	if t.verbose {
		fmt.Printf("📊 Found %d contracts to analyze for token metadata\n", len(contractAddresses))
	}

	// Get resolved contracts data from ABI resolver for additional context
	resolvedContracts, _ := baggage["resolved_contracts"].(map[string]*ContractInfo)

	// Create comprehensive contract information map
	contractMetadata := make(map[string]*TokenMetadata)
	allContractInfo := make(map[string]map[string]interface{})
	tokenCount := 0

	if t.verbose {
		fmt.Println("🔄 Analyzing contracts for token properties...")
	}

	// Get progress tracker from baggage if available
	progressTracker, hasProgress := baggage["progress_tracker"].(*models.ProgressTracker)

	// Test each contract address individually
	for i, address := range contractAddresses {
		// Send progress updates for each contract
		if hasProgress {
			progress := fmt.Sprintf("Analyzing contract %d of %d (%s)", i+1, len(contractAddresses), address[:10]+"...")
			progressTracker.UpdateComponent("token_metadata_enricher", models.ComponentGroupEnrichment, "Fetching Token Metadata", models.ComponentStatusRunning, progress)
		}

		if t.verbose {
			fmt.Printf("   [%d/%d] Analyzing %s...", i+1, len(contractAddresses), address)
		}

		contractInfo := make(map[string]interface{})
		hasAnyTokenLikeData := false

		// Get ABI resolver data first (this is the most authoritative)
		var abiContract *ContractInfo
		if resolvedContracts != nil {
			abiContract, _ = resolvedContracts[strings.ToLower(address)]
		}

		// Send sub-progress update for ABI data
		if hasProgress {
			progress := fmt.Sprintf("Processing ABI data for %s...", address[:10]+"...")
			progressTracker.UpdateComponent("token_metadata_enricher", models.ComponentGroupEnrichment, "Fetching Token Metadata", models.ComponentStatusRunning, progress)
		}

		// Add ABI resolver information (contract name from verification, etc.)
		if abiContract != nil && abiContract.IsVerified {
			if abiContract.ContractName != "" {
				contractInfo["verified_name"] = abiContract.ContractName
			}
			if abiContract.CompilerVersion != "" {
				contractInfo["compiler_version"] = abiContract.CompilerVersion
			}
			if abiContract.IsProxy {
				contractInfo["is_proxy"] = true
				if abiContract.Implementation != "" {
					contractInfo["implementation"] = abiContract.Implementation
				}
			}
			contractInfo["is_verified"] = true
			contractInfo["source_verified"] = true
		}

		// Send sub-progress update for RPC calls
		if hasProgress {
			progress := fmt.Sprintf("Making RPC calls to %s...", address[:10]+"...")
			progressTracker.UpdateComponent("token_metadata_enricher", models.ComponentGroupEnrichment, "Fetching Token Metadata", models.ComponentStatusRunning, progress)
		}

		// Get RPC contract information (method calls, etc.)
		var rpcInfo *rpc.ContractInfo
		var err error

		// Check cache first if available
		if t.cache != nil {
			networkID := int64(1) // Default to Ethereum mainnet
			if rawData, ok := baggage["raw_data"].(map[string]interface{}); ok {
				if nid, ok := rawData["network_id"].(float64); ok {
					networkID = int64(nid)
				}
			}

			cacheKey := fmt.Sprintf(TokenMetadataKeyPattern, networkID, strings.ToLower(address))
			if err := t.cache.GetJSON(ctx, cacheKey, &rpcInfo); err == nil {
				if t.verbose {
					fmt.Printf(" ✅ (cached) Name: %s, Symbol: %s, Decimals: %d\n", rpcInfo.Name, rpcInfo.Symbol, rpcInfo.Decimals)
				}
			} else {
				// Cache miss, make RPC call
				rpcInfo, err = t.getRPCContractInfo(ctx, address)
				if err != nil {
					if t.verbose {
						fmt.Printf(" ❌ RPC call failed: %v\n", err)
					}
				} else {
					// Cache successful result with permanent TTL (token metadata doesn't change)
					if cacheErr := t.cache.SetJSON(ctx, cacheKey, rpcInfo, &MetadataTTLDuration); cacheErr != nil {
						if t.verbose {
							fmt.Printf(" ⚠️ Cache store failed: %v\n", cacheErr)
						}
					}
				}
			}
		} else {
			// No cache, make RPC call directly
			rpcInfo, err = t.getRPCContractInfo(ctx, address)
			if err != nil {
				if t.verbose {
					fmt.Printf(" ❌ RPC call failed: %v\n", err)
				}
			}
		}

		if err == nil && rpcInfo != nil {
			// Extract RPC information
			if rpcInfo.Name != "" {
				contractInfo["rpc_name"] = rpcInfo.Name
				hasAnyTokenLikeData = true
			}

			if rpcInfo.Symbol != "" {
				contractInfo["rpc_symbol"] = rpcInfo.Symbol
				hasAnyTokenLikeData = true
			}

			if rpcInfo.Decimals >= 0 {
				contractInfo["rpc_decimals"] = rpcInfo.Decimals
				hasAnyTokenLikeData = true
			}

			if rpcInfo.TotalSupply != "" && rpcInfo.TotalSupply != "0" {
				contractInfo["rpc_total_supply"] = rpcInfo.TotalSupply
				hasAnyTokenLikeData = true
			}

			// Add supported interfaces from RPC metadata
			if supportedInterfaces, ok := rpcInfo.Metadata["supported_interfaces"].([]string); ok && len(supportedInterfaces) > 0 {
				contractInfo["supported_interfaces"] = supportedInterfaces
			}

			// Add available methods from RPC metadata
			if availableMethods, ok := rpcInfo.Metadata["available_methods"].([]string); ok && len(availableMethods) > 0 {
				contractInfo["available_methods"] = availableMethods
			}

			// Add RPC debug info
			if rpcDebug, ok := rpcInfo.Metadata["rpc_debug"].(string); ok {
				contractInfo["rpc_debug"] = rpcDebug
			}

			if t.verbose {
				if hasAnyTokenLikeData {
					fmt.Printf(" ✅ Token detected")
					if rpcInfo.Symbol != "" {
						fmt.Printf(" (%s)", rpcInfo.Symbol)
					}
					fmt.Println()
				} else {
					fmt.Printf(" ⚪ Not a token contract\n")
				}
			}
		}

		// Send sub-progress update for CoinMarketCap API calls
		if hasProgress {
			progress := fmt.Sprintf("Fetching CoinMarketCap metadata for %s...", address[:10]+"...")
			progressTracker.UpdateComponent("token_metadata_enricher", models.ComponentGroupEnrichment, "Fetching Token Metadata", models.ComponentStatusRunning, progress)
		}

		// Try to get additional metadata from CoinMarketCap API
		if t.cmcClient.IsAvailable() && hasAnyTokenLikeData {
			cmcInfo, err := t.cmcClient.GetTokenInfo(ctx, address)
			if err == nil && cmcInfo != nil {
				contractInfo["cmc_name"] = cmcInfo.Name
				contractInfo["cmc_symbol"] = cmcInfo.Symbol
				contractInfo["cmc_logo"] = cmcInfo.Logo
				contractInfo["cmc_description"] = cmcInfo.Description
				contractInfo["cmc_website"] = cmcInfo.Website
				contractInfo["cmc_category"] = cmcInfo.Category

				if t.verbose {
					fmt.Printf(" 🪙 CoinMarketCap data: %s (%s)", cmcInfo.Name, cmcInfo.Symbol)
					if cmcInfo.Logo != "" {
						fmt.Printf(" [Logo: ✅]")
					}
					fmt.Println()
				}
			}
		}

		// Determine the best name and symbol to use (prioritize verified data, then CMC, then RPC)
		var bestName, bestSymbol, bestLogo, bestDescription, bestWebsite, bestCategory string
		var bestDecimals int = -1

		// Priority: CMC name > verified ABI name > RPC name
		if cmcName, ok := contractInfo["cmc_name"].(string); ok && cmcName != "" {
			bestName = cmcName
		} else if verifiedName, ok := contractInfo["verified_name"].(string); ok && verifiedName != "" {
			bestName = verifiedName
		} else if rpcName, ok := contractInfo["rpc_name"].(string); ok && rpcName != "" {
			bestName = rpcName
		}

		// Priority: CMC symbol > RPC symbol
		if cmcSymbol, ok := contractInfo["cmc_symbol"].(string); ok && cmcSymbol != "" {
			bestSymbol = cmcSymbol
		} else if rpcSymbol, ok := contractInfo["rpc_symbol"].(string); ok && rpcSymbol != "" {
			bestSymbol = rpcSymbol
		}

		// Get best decimals from RPC (most authoritative for on-chain data)
		if rpcDecimals, ok := contractInfo["rpc_decimals"].(int); ok {
			bestDecimals = rpcDecimals
		}

		// Get additional CoinMarketCap metadata
		if cmcLogo, ok := contractInfo["cmc_logo"].(string); ok && cmcLogo != "" {
			bestLogo = cmcLogo
		}
		if cmcDescription, ok := contractInfo["cmc_description"].(string); ok && cmcDescription != "" {
			bestDescription = cmcDescription
		}
		if cmcWebsite, ok := contractInfo["cmc_website"].(string); ok && cmcWebsite != "" {
			bestWebsite = cmcWebsite
		}
		if cmcCategory, ok := contractInfo["cmc_category"].(string); ok && cmcCategory != "" {
			bestCategory = cmcCategory
		}

		// Determine token type based on available methods and responses
		tokenType := "Contract"
		if bestName != "" || bestSymbol != "" {
			if bestDecimals > 0 {
				tokenType = "ERC20" // Has name/symbol and decimals - this is an ERC20 token
			} else {
				tokenType = "ERC721" // Has name/symbol but no decimals - likely ERC721
			}
		}

		// Store all discovered information
		allContractInfo[address] = contractInfo

		// Create TokenMetadata for contracts that have token-like characteristics
		// ONLY add contracts that actually have token-like methods, not just any verified contract
		if hasAnyTokenLikeData {
			metadata := &TokenMetadata{
				Address:     address,
				Type:        tokenType, // Use determined token type instead of generic "Contract"
				Name:        bestName,
				Symbol:      bestSymbol,
				Decimals:    bestDecimals,
				Logo:        bestLogo,
				Description: bestDescription,
				Website:     bestWebsite,
				Category:    bestCategory,
			}
			contractMetadata[address] = metadata
			tokenCount++
		}
	}

	if t.verbose {
		fmt.Printf("✅ Token metadata enrichment complete. Found %d token contracts.\n", tokenCount)
		fmt.Println(strings.Repeat("🪙", 60) + "\n")
	}

	// Add to baggage
	if len(contractMetadata) > 0 {
		baggage["token_metadata"] = contractMetadata
	}

	// Only store debug information in DEBUG mode to avoid overwhelming baggage
	if os.Getenv("DEBUG") == "true" {
		// Add all contract information to baggage for debug purposes only
		if len(allContractInfo) > 0 {
			baggage["all_contract_info"] = allContractInfo
		}
	}

	return nil
}

// Helper functions
func getStringValue(info map[string]interface{}, key string) string {
	if val, ok := info[key].(string); ok {
		return val
	}
	return ""
}

// GetPromptContext provides context for the LLM prompt
func (t *TokenMetadataEnricher) GetPromptContext(ctx context.Context, baggage map[string]interface{}) string {
	tokenMetadata, ok := baggage["token_metadata"].(map[string]*TokenMetadata)
	if !ok || len(tokenMetadata) == 0 {
		return ""
	}

	var contextParts []string
	contextParts = append(contextParts, "Token Metadata:")

	for address, metadata := range tokenMetadata {
		if metadata.Name != "" && metadata.Symbol != "" {
			tokenInfo := fmt.Sprintf("- %s (%s): Contract %s", metadata.Name, metadata.Symbol, address)

			if metadata.Decimals > 0 {
				tokenInfo += fmt.Sprintf(", %d decimals", metadata.Decimals)
			}

			if metadata.Type != "" {
				tokenInfo += fmt.Sprintf(", Type: %s", metadata.Type)
			}

			if metadata.Category != "" {
				tokenInfo += fmt.Sprintf(", Category: %s", metadata.Category)
			}

			if metadata.Description != "" {
				tokenInfo += fmt.Sprintf(", Description: %s", metadata.Description)
			}

			if metadata.Website != "" {
				tokenInfo += fmt.Sprintf(", Website: %s", metadata.Website)
			}

			if metadata.Logo != "" {
				tokenInfo += fmt.Sprintf(", Logo: %s", metadata.Logo)
			}

			contextParts = append(contextParts, tokenInfo)
		}
	}

	return strings.Join(contextParts, "\n")
}

// getRPCContractInfo calls GetContractInfo once and returns the result
func (t *TokenMetadataEnricher) getRPCContractInfo(ctx context.Context, address string) (*rpc.ContractInfo, error) {
	if t.rpcClient == nil {
		return nil, fmt.Errorf("no RPC client available")
	}

	return t.rpcClient.GetContractInfo(ctx, address)
}

// GetRagContext provides RAG context for token metadata information
func (t *TokenMetadataEnricher) GetRagContext(ctx context.Context, baggage map[string]interface{}) *RagContext {
	ragContext := NewRagContext()

	tokenMetadata, ok := baggage["token_metadata"].(map[string]*TokenMetadata)
	if !ok || len(tokenMetadata) == 0 {
		return ragContext
	}

	// Add token metadata to RAG context for searchability
	for address, metadata := range tokenMetadata {
		if metadata.Name != "" && metadata.Symbol != "" {
			ragContext.AddItem(RagContextItem{
				ID:      fmt.Sprintf("token_%s", address),
				Type:    "token",
				Title:   fmt.Sprintf("%s (%s) Token", metadata.Name, metadata.Symbol),
				Content: fmt.Sprintf("Token %s (%s) at address %s has %d decimals and is of type %s", metadata.Name, metadata.Symbol, address, metadata.Decimals, metadata.Type),
				Metadata: map[string]interface{}{
					"address":  address,
					"name":     metadata.Name,
					"symbol":   metadata.Symbol,
					"decimals": metadata.Decimals,
					"type":     metadata.Type,
				},
				Keywords:  []string{metadata.Name, metadata.Symbol, address, "token", metadata.Type},
				Relevance: 0.8,
			})
		}
	}

	return ragContext
}
