package tools

import (
	"log"

	"github.com/txplain/txplain/internal/data"
)

// CacheIntegrationExample demonstrates how to integrate caching into the txplain pipeline
// This is an example - you would integrate this into agent.go or wherever tools are initialized

func ExampleCacheIntegration() error {
	// This is a simplified example showing cache integration
	// In reality, you would get the connector from your application configuration

	// 1. Assume you have a data connector initialized (PostgreSQL, Redis, etc.)
	var connector data.Connector = nil // Replace with actual connector

	// 2. Create cache instance with appropriate prefix and default TTL
	cache := NewSimpleCache(connector, "txplain", &MetadataTTLDuration)

	// 3. Initialize tools with cache
	abiResolver := NewABIResolver()
	abiResolver.SetCache(cache)

	signatureResolver := NewSignatureResolver()
	signatureResolver.SetCache(cache)

	erc20PriceLookup := NewERC20PriceLookup("")
	erc20PriceLookup.SetCache(cache)

	ensResolver := NewENSResolver()
	ensResolver.SetCache(cache)

	log.Printf("✅ Cache integration example completed successfully")
	log.Printf("📊 Tools configured with caching:")
	log.Printf("   - ABI Resolver: Contract ABIs, function/event signatures")
	log.Printf("   - Signature Resolver: 4byte.directory lookups")
	log.Printf("   - ERC20 Price Lookup: Token prices and CMC mappings")
	log.Printf("   - ENS Resolver: Address-to-name mappings")
	log.Printf("   - Cache TTLs: ABIs (60 days), Prices (1 hour), ENS (60 days)")

	return nil
}

// Cache key examples for reference:
//
// ABI caching:
//   - contract-abi:1:0x123... (full contract ABI)
//   - abi-func-sig:1:0x12345678 (individual function signature)
//   - abi-event-sig:1:0xddf252ad... (individual event signature)
//
// Signature caching:
//   - 4byte-func-sig:0x12345678 (4byte.directory function lookup)
//   - 4byte-event-sig:0xddf252ad... (4byte.directory event lookup)
//
// Price caching:
//   - erc20-price:1:0x123... (token price by contract address)
//   - cmc-token-map:0:symbol:name:address (CoinMarketCap token ID mapping)
//
// ENS caching:
//   - ens-name:1:0x123... (address to ENS name mapping)
//
// Network caching:
//   - network-info:1 (network metadata)
//   - cmc-network:ethereum (CoinMarketCap network mapping) 