package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/txplain/txplain/internal/data"
)

// Cache provides a simple key-value cache interface for tools
type Cache interface {
	// Get retrieves a value by key, returns nil if not found
	Get(ctx context.Context, key string) ([]byte, error)

	// Set stores a value with optional TTL
	Set(ctx context.Context, key string, value []byte, ttl *time.Duration) error

	// GetJSON retrieves and unmarshals JSON data
	GetJSON(ctx context.Context, key string, dest interface{}) error

	// SetJSON marshals and stores JSON data
	SetJSON(ctx context.Context, key string, value interface{}, ttl *time.Duration) error

	// Delete removes a key (if supported)
	Delete(ctx context.Context, key string) error

	// Has checks if a key exists
	Has(ctx context.Context, key string) bool
}

// SimpleCache implements Cache using a data.Connector
type SimpleCache struct {
	connector  data.Connector
	defaultTTL *time.Duration
	keyPrefix  string
}

// NewSimpleCache creates a new cache instance
func NewSimpleCache(connector data.Connector, keyPrefix string, defaultTTL *time.Duration) *SimpleCache {
	return &SimpleCache{
		connector:  connector,
		defaultTTL: defaultTTL,
		keyPrefix:  keyPrefix,
	}
}

// formatKey adds prefix to avoid collisions
func (c *SimpleCache) formatKey(key string) string {
	if c.keyPrefix == "" {
		return key
	}
	return fmt.Sprintf("%s:%s", c.keyPrefix, key)
}

// Get retrieves a value by key
func (c *SimpleCache) Get(ctx context.Context, key string) ([]byte, error) {
	formattedKey := c.formatKey(key)
	// Use empty string for index (not used in simple key-value)
	// Use key as both partition and range key for simplicity
	return c.connector.Get(ctx, "", formattedKey, "default")
}

// Set stores a value with optional TTL
func (c *SimpleCache) Set(ctx context.Context, key string, value []byte, ttl *time.Duration) error {
	formattedKey := c.formatKey(key)

	// Use provided TTL or default
	cacheTTL := ttl
	if cacheTTL == nil && c.defaultTTL != nil {
		cacheTTL = c.defaultTTL
	}

	return c.connector.Set(ctx, formattedKey, "default", value, cacheTTL)
}

// GetJSON retrieves and unmarshals JSON data
func (c *SimpleCache) GetJSON(ctx context.Context, key string, dest interface{}) error {
	data, err := c.Get(ctx, key)
	if err != nil {
		return err
	}
	if data == nil {
		return fmt.Errorf("key not found: %s", key)
	}
	return json.Unmarshal(data, dest)
}

// SetJSON marshals and stores JSON data
func (c *SimpleCache) SetJSON(ctx context.Context, key string, value interface{}, ttl *time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}
	return c.Set(ctx, key, data, ttl)
}

// Delete removes a key by setting empty value with immediate expiry
func (c *SimpleCache) Delete(ctx context.Context, key string) error {
	immediateExpiry := time.Nanosecond
	return c.Set(ctx, key, []byte{}, &immediateExpiry)
}

// Has checks if a key exists
func (c *SimpleCache) Has(ctx context.Context, key string) bool {
	_, err := c.Get(ctx, key)
	return err == nil
}

// Standard TTL durations for different types of data
var (
	// ABIs and signatures rarely change - permanent data
	ABITTLDuration = time.Hour * 24 * 365 // 1 year (effectively permanent)

	// Token prices change frequently - 1 hour TTL as requested
	PriceTTLDuration = time.Hour * 1 // 1 hour

	// Token metadata (symbol, name, decimals) is permanent - no TTL
	MetadataTTLDuration = time.Hour * 24 * 365 // 1 year (effectively permanent)

	// ENS names change rarely but can change
	ENSTTLDuration = time.Hour * 24 * 30 // 30 days

	// Icon URLs are permanent once set
	IconTTLDuration = time.Hour * 24 * 365 // 1 year (effectively permanent)

	// Network data is permanent
	NetworkTTLDuration = time.Hour * 24 * 365 // 1 year (effectively permanent)

	// Signature data is permanent
	SignatureTTLDuration = time.Hour * 24 * 365 // 1 year (effectively permanent)
)

// Cache key patterns for consistent naming - includes network ID for uniqueness
const (
	// ABI caching - format: contract-abi:networkId:address
	ABIKeyPattern         = "contract-abi:%d:%s"  // contract-abi:1:0x123...
	ABIFunctionKeyPattern = "abi-func-sig:%d:%s"  // abi-func-sig:1:0x12345678
	ABIEventKeyPattern    = "abi-event-sig:%d:%s" // abi-event-sig:1:0xddf252ad...

	// 4byte signature caching - format: 4byte-sig:type:hash
	FunctionSigKeyPattern = "4byte-func-sig:%s"  // 4byte-func-sig:0x12345678
	EventSigKeyPattern    = "4byte-event-sig:%s" // 4byte-event-sig:0xddf252ad...

	// Token price caching - format: erc20-price:networkId:address
	TokenPriceKeyPattern = "erc20-price:%d:%s"   // erc20-price:1:0x123...
	CMCMappingKeyPattern = "cmc-token-map:%d:%s" // cmc-token-map:1:0x123...

	// ENS caching - format: ens-name:networkId:address or ens-addr:networkId:name
	ENSNameKeyPattern    = "ens-name:%d:%s" // ens-name:1:0x123...
	ENSAddressKeyPattern = "ens-addr:%d:%s" // ens-addr:1:vitalik.eth

	// Token metadata caching - format: token-meta:networkId:address
	TokenMetadataKeyPattern = "token-meta:%d:%s" // token-meta:1:0x123...

	// Icon caching - format: token-icon:networkId:address
	TokenIconKeyPattern = "token-icon:%d:%s" // token-icon:1:0x123...

	// Network caching - format: network-info:chainId
	NetworkKeyPattern    = "network-info:%d" // network-info:1
	CMCNetworkKeyPattern = "cmc-network:%s"  // cmc-network:ethereum
)
