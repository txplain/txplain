package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/txplain/txplain/internal/tools"
)

func main() {
	// Get API key from environment
	apiKey := os.Getenv("COINMARKETCAP_API_KEY")
	if apiKey == "" {
		log.Fatal("COINMARKETCAP_API_KEY environment variable not set")
	}

	fmt.Printf("Testing GrowAI token price lookup...\n")
	fmt.Printf("API Key loaded: %s...\n", apiKey[:10])

	// Create price lookup tool
	priceLookup := tools.NewERC20PriceLookup(apiKey)

	// Test GrowAI token
	growAIContract := "0x1c9b158e71bc274ea5519ca57a73e337cac72b3a"
	networkID := float64(1) // Ethereum mainnet

	fmt.Printf("\n=== Testing GrowAI Token ===\n")
	fmt.Printf("Contract: %s\n", growAIContract)
	fmt.Printf("Network: %.0f (Ethereum)\n", networkID)

	// Test input parameters
	input := map[string]interface{}{
		"contract_address": growAIContract,
		"network_id":       networkID,
		"symbol":           "GrowAI", // Try with symbol
	}

	ctx := context.Background()
	result, err := priceLookup.Run(ctx, input)
	
	if err != nil {
		fmt.Printf("❌ Error: %v\n", err)
	} else {
		if tokenData, ok := result["token"].(*tools.TokenPrice); ok {
			fmt.Printf("✅ Success!\n")
			fmt.Printf("  Name: %s\n", tokenData.Name)
			fmt.Printf("  Symbol: %s\n", tokenData.Symbol)
			fmt.Printf("  Price: $%.6f\n", tokenData.Price)
			fmt.Printf("  Price Source: %s\n", tokenData.PriceSource)
			fmt.Printf("  Has DEX Data: %t\n", tokenData.HasDEXData)
			
			if tokenData.HasDEXData {
				fmt.Printf("  DEX Price: $%.6f\n", tokenData.DEXPrice)
				fmt.Printf("  DEX Liquidity: $%.2f\n", tokenData.DEXLiquidity)
				fmt.Printf("  DEX Volume 24h: $%.2f\n", tokenData.DEXVolume24h)
				fmt.Printf("  DEX Pairs: %d\n", len(tokenData.DEXData))
				
				if len(tokenData.DEXData) > 0 {
					fmt.Printf("  Top DEX: %s (Liquidity: $%.2f)\n", 
						tokenData.DEXData[0].DexName, tokenData.DEXData[0].LiquidityUSD)
				}
			}
		} else {
			fmt.Printf("❌ Invalid response format\n")
		}
	}

	// Also test without symbol to see if it makes a difference
	fmt.Printf("\n=== Testing GrowAI Token (no symbol) ===\n")
	inputNoSymbol := map[string]interface{}{
		"contract_address": growAIContract,
		"network_id":       networkID,
	}

	result2, err2 := priceLookup.Run(ctx, inputNoSymbol)
	
	if err2 != nil {
		fmt.Printf("❌ Error: %v\n", err2)
	} else {
		if tokenData, ok := result2["token"].(*tools.TokenPrice); ok {
			fmt.Printf("✅ Success!\n")
			fmt.Printf("  Name: %s\n", tokenData.Name)
			fmt.Printf("  Symbol: %s\n", tokenData.Symbol)
			fmt.Printf("  Price: $%.6f\n", tokenData.Price)
			fmt.Printf("  Price Source: %s\n", tokenData.PriceSource)
			fmt.Printf("  Has DEX Data: %t\n", tokenData.HasDEXData)
		} else {
			fmt.Printf("❌ Invalid response format\n")
		}
	}

	// Test a few variations of the symbol
	fmt.Printf("\n=== Testing Symbol Variations ===\n")
	symbols := []string{"GrowAI", "GROAI", "SocialGrowAI", "SGrowAI"}
	
	for _, symbol := range symbols {
		fmt.Printf("Testing with symbol '%s':\n", symbol)
		testInput := map[string]interface{}{
			"contract_address": growAIContract,
			"network_id":       networkID,
			"symbol":           symbol,
		}
		
		result, err := priceLookup.Run(ctx, testInput)
		if err != nil {
			fmt.Printf("  ❌ %v\n", err)
		} else {
			if tokenData, ok := result["token"].(*tools.TokenPrice); ok {
				fmt.Printf("  ✅ Price: $%.6f (%s)\n", tokenData.Price, tokenData.PriceSource)
			}
		}
	}
} 