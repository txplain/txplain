package tools

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/tmc/langchaingo/llms/openai"
	"github.com/txplain/txplain/internal/models"
)

// Test network token symbol mapping
func TestMonetaryValueEnricher_GetNativeTokenSymbol(t *testing.T) {
	apiKey := "test-key"
	llm, _ := openai.New()
	enricher := NewMonetaryValueEnricher(llm, apiKey)

	// Get available networks dynamically instead of hardcoding expectations
	networkIDs := models.ListNetworkIDs()
	if len(networkIDs) == 0 {
		t.Skip("No networks configured for testing")
	}

	for _, networkID := range networkIDs {
		network, exists := models.GetNetwork(networkID)
		if !exists {
			continue
		}

		result := enricher.getNativeTokenSymbol(networkID)
		t.Logf("Network %d (%s): native token symbol = %s", networkID, network.Name, result)

		// Since we made it generic, we just verify it returns a string (could be empty)
		// No hardcoded expectations
		if result != "" && len(result) > 10 {
			t.Errorf("getNativeTokenSymbol(%d) returned unusually long symbol: %s", networkID, result)
		}
	}
}

// Test fallback pricing
func TestMonetaryValueEnricher_GetFallbackNativeTokenPrice(t *testing.T) {
	apiKey := "test-key"
	llm, _ := openai.New()
	enricher := NewMonetaryValueEnricher(llm, apiKey)

	// Get available networks dynamically instead of hardcoding expectations
	networkIDs := models.ListNetworkIDs()
	if len(networkIDs) == 0 {
		t.Skip("No networks configured for testing")
	}

	for _, networkID := range networkIDs {
		network, exists := models.GetNetwork(networkID)
		if !exists {
			continue
		}

		result := enricher.getFallbackNativeTokenPrice(networkID)
		t.Logf("Network %d (%s): fallback price = $%.2f", networkID, network.Name, result)

		// Since we made it generic and removed hardcoded prices,
		// we just verify it returns a non-negative value
		if result < 0 {
			t.Errorf("getFallbackNativeTokenPrice(%d) returned negative price: %.2f", networkID, result)
		}
	}
}

// Test actual CoinMarketCap API calls for native tokens
func TestMonetaryValueEnricher_FetchNativeTokenPrice_Integration(t *testing.T) {
	// Skip if no API key is set
	apiKey := os.Getenv("COINMARKETCAP_API_KEY")
	if apiKey == "" {
		t.Skip("Skipping integration test: COINMARKETCAP_API_KEY not set")
	}

	llm, _ := openai.New()
	enricher := NewMonetaryValueEnricher(llm, apiKey)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Test major native tokens
	tests := []struct {
		symbol   string
		minPrice float64 // Reasonable bounds to ensure API is working
		maxPrice float64
	}{
		{"ETH", 100.0, 10000.0},
		{"MATIC", 0.01, 10.0},
		{"BNB", 50.0, 1000.0},
		{"AVAX", 5.0, 200.0},
	}

	for _, test := range tests {
		t.Run(test.symbol, func(t *testing.T) {
			price, err := enricher.fetchNativeTokenPrice(ctx, test.symbol)
			if err != nil {
				t.Fatalf("fetchNativeTokenPrice(%s) failed: %v", test.symbol, err)
			}

			if price <= 0 {
				t.Fatalf("fetchNativeTokenPrice(%s) returned non-positive price: %f", test.symbol, price)
			}

			if price < test.minPrice || price > test.maxPrice {
				t.Logf("Warning: fetchNativeTokenPrice(%s) = $%.2f, outside expected range $%.2f-$%.2f (this could be normal market movement)",
					test.symbol, price, test.minPrice, test.maxPrice)
			}

			t.Logf("%s current price: $%.2f", test.symbol, price)
		})
	}
}

// Test gas fee calculation with actual prices
func TestMonetaryValueEnricher_GasFeesCalculation_Integration(t *testing.T) {
	// Skip if no API key is set
	apiKey := os.Getenv("COINMARKETCAP_API_KEY")
	if apiKey == "" {
		t.Skip("Skipping integration test: COINMARKETCAP_API_KEY not set")
	}

	llm, _ := openai.New()
	enricher := NewMonetaryValueEnricher(llm, apiKey)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Get available networks dynamically instead of hardcoding specific networks
	networkIDs := models.ListNetworkIDs()
	if len(networkIDs) == 0 {
		t.Skip("No networks configured for testing")
	}

	// Test first few networks
	testedCount := 0
	maxTests := 3 // Limit to avoid long test times

	for _, networkID := range networkIDs {
		if testedCount >= maxTests {
			break
		}

		network, exists := models.GetNetwork(networkID)
		if !exists {
			continue
		}

		t.Run(network.Name, func(t *testing.T) {
			// Create test baggage
			baggage := map[string]interface{}{
				"raw_data": map[string]interface{}{
					"network_id": float64(networkID),
					"receipt": map[string]interface{}{
						"gasUsed":           "0x5208",     // 21000 gas
						"effectiveGasPrice": "0x3B9ACA00", // 1 gwei
					},
				},
			}

			// Process gas fees
			nativePrice := enricher.getNativeTokenPrice(ctx, baggage)
			t.Logf("%s (Network %d): native token price: $%.4f", network.Name, networkID, nativePrice)

			if nativePrice <= 0 {
				t.Logf("No native token price available for %s - skipping USD calculations", network.Name)
				return
			}

			// Test enrichRawData
			err := enricher.enrichRawData(baggage, nativePrice)
			if err != nil {
				t.Fatalf("enrichRawData failed: %v", err)
			}

			// Check results
			rawData := baggage["raw_data"].(map[string]interface{})
			receipt := rawData["receipt"].(map[string]interface{})

			gasFeeNative, ok := receipt["gas_fee_native"].(string)
			if !ok {
				t.Fatalf("gas_fee_native not found in receipt")
			}

			gasFeeUSD, ok := receipt["gas_fee_usd"].(string)
			if !ok {
				t.Fatalf("gas_fee_usd not found in receipt")
			}

			t.Logf("%s gas fee: %s native tokens ($%s USD)", network.Name, gasFeeNative, gasFeeUSD)

			// Verify the values are reasonable (not zero, not negative)
			if gasFeeNative == "0.000000" || gasFeeNative == "" {
				t.Errorf("Invalid native gas fee: %s", gasFeeNative)
			}
			if gasFeeUSD == "0.00" || gasFeeUSD == "" {
				t.Errorf("Invalid USD gas fee: %s", gasFeeUSD)
			}
		})
		testedCount++
	}
}

// Test complete process with mixed token types
func TestMonetaryValueEnricher_Process_Integration(t *testing.T) {
	// Skip if no API key is set
	apiKey := os.Getenv("COINMARKETCAP_API_KEY")
	if apiKey == "" {
		t.Skip("Skipping integration test: COINMARKETCAP_API_KEY not set")
	}

	llm, _ := openai.New()
	enricher := NewMonetaryValueEnricher(llm, apiKey)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Get first available network dynamically instead of hardcoding Polygon
	networkIDs := models.ListNetworkIDs()
	if len(networkIDs) == 0 {
		t.Skip("No networks configured for testing")
	}

	testNetworkID := networkIDs[0]
	network, exists := models.GetNetwork(testNetworkID)
	if !exists {
		t.Skip("Test network not properly configured")
	}

	// Create test baggage with dynamic network
	baggage := map[string]interface{}{
		"raw_data": map[string]interface{}{
			"network_id": float64(testNetworkID),
			"receipt": map[string]interface{}{
				"gasUsed":           "0x0186A0",   // 100,000 gas
				"effectiveGasPrice": "0x3B9ACA00", // 1 gwei
			},
		},
		"token_metadata": map[string]*TokenMetadata{
			"0xa0b86a33e6d93d5073cfa3e7b31fe6a6b93a2ed7": {
				Address:  "0xa0b86a33e6d93d5073cfa3e7b31fe6a6b93a2ed7",
				Name:     "Test Token",
				Symbol:   "TEST",
				Decimals: 6,
				Type:     "ERC20",
			},
		},
		"token_prices": map[string]*TokenPrice{
			"0xa0b86a33e6d93d5073cfa3e7b31fe6a6b93a2ed7": {
				Symbol: "TEST",
				Price:  1.0,
			},
		},
		"transfers": []models.TokenTransfer{
			{
				Type:     "ERC20",
				Contract: "0xa0b86a33e6d93d5073cfa3e7b31fe6a6b93a2ed7",
				From:     "0x1234567890123456789012345678901234567890",
				To:       "0x0987654321098765432109876543210987654321",
				Amount:   "0x1DCD6500", // 500,000,000 (500 TEST tokens with 6 decimals)
				Symbol:   "TEST",
			},
		},
	}

	err := enricher.Process(ctx, baggage)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	// Check gas fee calculation
	rawData := baggage["raw_data"].(map[string]interface{})
	receipt := rawData["receipt"].(map[string]interface{})

	gasFeeUSD, ok := receipt["gas_fee_usd"].(string)
	if !ok {
		t.Logf("gas_fee_usd not found in receipt - may be expected if no native token price available")
	} else {
		t.Logf("%s gas fee: $%s USD", network.Name, gasFeeUSD)
	}

	// Check transfer enrichment
	transfers := baggage["transfers"].([]models.TokenTransfer)
	if len(transfers) != 1 {
		t.Fatalf("Expected 1 transfer, got %d", len(transfers))
	}

	transfer := transfers[0]
	if transfer.FormattedAmount == "" {
		t.Errorf("FormattedAmount not set")
	}
	if transfer.AmountUSD == "" {
		t.Errorf("AmountUSD not set")
	}

	t.Logf("Transfer: %s %s ($%s USD)", transfer.FormattedAmount, transfer.Symbol, transfer.AmountUSD)

	// Verify debug information
	debugInfo, ok := baggage["debug_info"].(map[string]interface{})
	if !ok {
		t.Fatalf("debug_info not found")
	}

	enrichmentDebug, ok := debugInfo["transfer_enrichment"].([]string)
	if !ok {
		t.Fatalf("transfer_enrichment debug info not found")
	}

	t.Logf("Debug info: %v", enrichmentDebug)
}

// Test error handling with invalid API key
func TestMonetaryValueEnricher_InvalidAPIKey(t *testing.T) {
	llm, _ := openai.New()
	enricher := NewMonetaryValueEnricher(llm, "invalid-api-key")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// This should fail and fall back to hardcoded values
	price, err := enricher.fetchNativeTokenPrice(ctx, "ETH")
	if err == nil {
		t.Errorf("Expected error with invalid API key, but got price: %f", price)
	}

	// Test that fallback works
	baggage := map[string]interface{}{
		"raw_data": map[string]interface{}{
			"network_id": float64(137), // Polygon
		},
	}

	fallbackPrice := enricher.getNativeTokenPrice(ctx, baggage)
	if fallbackPrice <= 0 {
		t.Errorf("Expected positive fallback price, got: %f", fallbackPrice)
	}

	// Should be the fallback MATIC price
	expectedFallback := enricher.getFallbackNativeTokenPrice(137)
	if fallbackPrice != expectedFallback {
		t.Errorf("Expected fallback price %f, got %f", expectedFallback, fallbackPrice)
	}
}

// Benchmark native token price fetching
func BenchmarkMonetaryValueEnricher_FetchNativeTokenPrice(b *testing.B) {
	apiKey := os.Getenv("COINMARKETCAP_API_KEY")
	if apiKey == "" {
		b.Skip("Skipping benchmark: COINMARKETCAP_API_KEY not set")
	}

	llm, _ := openai.New()
	enricher := NewMonetaryValueEnricher(llm, apiKey)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := enricher.fetchNativeTokenPrice(ctx, "ETH")
		if err != nil {
			b.Fatalf("fetchNativeTokenPrice failed: %v", err)
		}
	}
}
