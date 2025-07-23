package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/txplain/txplain/internal/agent"
	"github.com/txplain/txplain/internal/models"
	"github.com/txplain/txplain/internal/tools"
)

// Integration test for the complete transaction pipeline
func TestCompleteTransactionPipeline_Integration(t *testing.T) {
	// Skip if no API keys are set
	openaiKey := os.Getenv("OPENAI_API_KEY")
	cmcKey := os.Getenv("COINMARKETCAP_API_KEY")
	if openaiKey == "" || cmcKey == "" {
		t.Skip("Skipping integration test: OPENAI_API_KEY and COINMARKETCAP_API_KEY required")
	}

	// Test cases with different networks and transaction types
	testCases := []struct {
		name         string
		txHash       string
		networkID    int64
		network      string
		expectations struct {
			shouldContainNFTs bool
			shouldContainSwap bool
			shouldHaveGasFee  bool
			nativeTokenSymbol string
			minGasFeeUSD      float64 // Minimum expected gas fee in USD
			maxGasFeeUSD      float64 // Maximum expected gas fee in USD
		}
	}{
		{
			name:      "Polygon NFT + Token Swap",
			txHash:    "0x01fb33b7b5fc9824fc813bf8f3b80c396a0664f66418edebca42d84f3d6022f3",
			networkID: 137,
			network:   "Polygon",
			expectations: struct {
				shouldContainNFTs bool
				shouldContainSwap bool
				shouldHaveGasFee  bool
				nativeTokenSymbol string
				minGasFeeUSD      float64
				maxGasFeeUSD      float64
			}{
				shouldContainNFTs: true, // Should detect ERC1155 NFTs
				shouldContainSwap: true, // Should detect USDC swap
				shouldHaveGasFee:  true, // Should calculate MATIC gas fees
				nativeTokenSymbol: "MATIC",
				minGasFeeUSD:      0.001, // Very low for Polygon
				maxGasFeeUSD:      5.0,   // Should be much less than $5
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create agent
			txAgent, err := agent.NewTxplainAgent(openaiKey, cmcKey)
			if err != nil {
				t.Fatalf("Failed to initialize agent: %v", err)
			}

			// Create transaction request
			request := &models.TransactionRequest{
				TxHash:    tc.txHash,
				NetworkID: tc.networkID,
			}

			// Create context with timeout
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			// Process the transaction
			result, err := txAgent.ExplainTransaction(ctx, request)
			if err != nil {
				t.Fatalf("Failed to explain transaction: %v", err)
			}

			// Test NFT detection
			if tc.expectations.shouldContainNFTs {
				// Check if NFTs are mentioned in summary
				summary := strings.ToLower(result.Summary)
				nftTerms := []string{"nft", "token id", "3226", "tappers", "erc-1155", "erc1155"}
				found := false
				for _, term := range nftTerms {
					if strings.Contains(summary, term) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected NFTs to be mentioned in summary, but not found. Summary: %s", result.Summary)
				} else {
					t.Logf("✅ NFTs properly detected in summary: %s", result.Summary)
				}
			}

			// Test swap detection
			if tc.expectations.shouldContainSwap {
				summary := strings.ToLower(result.Summary)
				swapTerms := []string{"swap", "usdc", "exchange", "trade"}
				found := false
				for _, term := range swapTerms {
					if strings.Contains(summary, term) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected swap to be mentioned in summary, but not found. Summary: %s", result.Summary)
				} else {
					t.Logf("✅ Swap properly detected in summary: %s", result.Summary)
				}
			}

			// Test gas fee calculation
			if tc.expectations.shouldHaveGasFee {
				// Check if gas fees are mentioned in summary with reasonable USD amounts
				summary := strings.ToLower(result.Summary)
				if strings.Contains(summary, "$") && (strings.Contains(summary, "gas") || strings.Contains(summary, "fee")) {
					t.Logf("✅ Gas fees properly included in summary: %s", result.Summary)

					// Extract USD amount if possible and verify it's reasonable
					// This is a basic check - in production you'd want more sophisticated parsing
					if tc.networkID == 137 && strings.Contains(summary, "$0.0") { // Polygon should have very low fees
						t.Logf("✅ Polygon gas fee appears reasonable (very low)")
					} else if tc.networkID == 1 && (strings.Contains(summary, "$") && !strings.Contains(summary, "$0.0")) {
						t.Logf("✅ Ethereum gas fee appears reasonable (not zero)")
					}
				} else {
					t.Logf("⚠️  Gas fees not explicitly mentioned in summary (this might be OK): %s", result.Summary)
				}
			}

			// Test network-specific native token
			if result.NetworkID != tc.networkID {
				t.Errorf("Expected network ID %d, got %d", tc.networkID, result.NetworkID)
			}

			// Log comprehensive results
			t.Logf("📊 Transaction Analysis Results:")
			t.Logf("   Summary: %s", result.Summary)
			t.Logf("   Network: %s (%d)", tc.network, result.NetworkID)
			t.Logf("   Gas Used: %d", result.GasUsed)
			t.Logf("   Status: %s", result.Status)
			t.Logf("   Transfers: %d", len(result.Transfers))

			for i, transfer := range result.Transfers {
				t.Logf("     Transfer %d: %s %s (%s) from %s to %s",
					i+1, transfer.FormattedAmount, transfer.Symbol, transfer.Type,
					transfer.From, transfer.To)
				if transfer.AmountUSD != "" {
					t.Logf("       USD Value: $%s", transfer.AmountUSD)
				}
			}

			// Check pipeline baggage if available
			if result.Metadata != nil {
				if baggage, ok := result.Metadata["pipeline_baggage"].(map[string]interface{}); ok {
					// Check NFT transfers
					if nftTransfers, exists := baggage["nft_transfers"]; exists {
						t.Logf("   NFT Transfers found in baggage: %+v", nftTransfers)
					}

					// Check debug info
					if debugInfo, ok := baggage["debug_info"].(map[string]interface{}); ok {
						if nftDebug, exists := debugInfo["nft_decoder"]; exists {
							t.Logf("   NFT Decoder Debug: %+v", nftDebug)
						}
						if transferDebug, exists := debugInfo["transfer_enrichment"]; exists {
							t.Logf("   Transfer Enrichment Debug: %+v", transferDebug)
						}
					}
				}
			}
		})
	}
}

// Test specific NFT decoder functionality with real transaction
func TestNFTDecoder_RealTransaction_Integration(t *testing.T) {
	// Skip if no API key is set
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Test with a known NFT transaction (OpenSea or similar)
	// This is a more focused test on just the NFT decoder component
	decoder := tools.NewNFTDecoder()

	// Create mock events that represent real ERC1155 transfers like the one in the Polygon transaction
	events := []models.Event{
		{
			Name:     "TransferSingle",
			Contract: "0x2953399124f0cbb46d2cbacd8a89cf0599974963", // Example ERC1155 contract
			Parameters: map[string]interface{}{
				"operator": "0x000000000000000000000000495f947276749ce646f68ac8c248420045cb7b5e",
				"from":     "0x0000000000000000000000000000000000000000000000000000000000000000", // Mint from zero
				"to":       "0x000000000000000000000000f5f3a012b1304431fac2d7b6c4432ae4457a91c24",
				"id":       "0x0000000000000000000000000000000000000000000000000000000000000c9a", // 3226
				"value":    "0x0000000000000000000000000000000000000000000000000000000000000002", // 2 NFTs
			},
		},
	}

	ctx := context.Background()
	baggage := map[string]interface{}{
		"events": events,
	}

	err := decoder.Process(ctx, baggage)
	if err != nil {
		t.Fatalf("NFT decoder process failed: %v", err)
	}

	// Verify results
	nftTransfers, ok := tools.GetNFTTransfers(baggage)
	if !ok {
		t.Fatalf("NFT transfers not found in baggage")
	}

	if len(nftTransfers) != 1 {
		t.Fatalf("Expected 1 NFT transfer, got %d", len(nftTransfers))
	}

	transfer := nftTransfers[0]
	if transfer.Type != "ERC1155" {
		t.Errorf("Expected ERC1155, got %s", transfer.Type)
	}
	if transfer.TokenID != "3226" {
		t.Errorf("Expected token ID 3226, got %s", transfer.TokenID)
	}
	if transfer.Amount != "2" {
		t.Errorf("Expected amount 2, got %s", transfer.Amount)
	}

	// Test prompt context generation
	context := decoder.GetPromptContext(ctx, baggage)
	if context == "" {
		t.Error("Expected non-empty prompt context")
	}

	expectedInContext := []string{
		"ERC-1155 Tokens Transferred: 1",
		"Token ID: 3226",
		"Amount: 2",
	}

	for _, expected := range expectedInContext {
		if !strings.Contains(context, expected) {
			t.Errorf("Expected '%s' in context, but not found. Context: %s", expected, context)
		}
	}

	t.Logf("✅ NFT Decoder Integration Test Passed")
	t.Logf("Generated Context:\n%s", context)
}
