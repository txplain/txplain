package models

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Network represents supported blockchain networks
type Network struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	RPCUrl   string `json:"rpc_url"`
	Explorer string `json:"explorer"`
}

// SupportedNetworks will be populated from environment variables or defaults
var SupportedNetworks map[int64]Network

// Default networks (used as fallback if no env vars are configured)
var defaultNetworks = map[int64]Network{
	1: {
		ID:       1,
		Name:     "Ethereum",
		RPCUrl:   "https://eth.llamarpc.com",
		Explorer: "https://etherscan.io",
	},
	137: {
		ID:       137,
		Name:     "Polygon",
		RPCUrl:   "https://polygon-rpc.com",
		Explorer: "https://polygonscan.com",
	},
	42161: {
		ID:       42161,
		Name:     "Arbitrum",
		RPCUrl:   "https://arb1.arbitrum.io/rpc",
		Explorer: "https://arbiscan.io",
	},
}

// NetworkConfig holds configuration for initializing networks
type NetworkConfig struct {
	networks map[int64]Network
}

// LoadNetworksFromEnv loads network configurations from environment variables
// Uses the pattern: RPC_ENDPOINT_CHAIN_<CHAIN_ID>, NETWORK_NAME_CHAIN_<CHAIN_ID>, EXPLORER_URL_CHAIN_<CHAIN_ID>
func LoadNetworksFromEnv() map[int64]Network {
	networks := make(map[int64]Network)
	
	// First, load defaults
	for id, network := range defaultNetworks {
		networks[id] = network
	}
	
	// Look for RPC endpoint environment variables
	for _, envVar := range os.Environ() {
		parts := strings.Split(envVar, "=")
		if len(parts) != 2 {
			continue
		}
		
		key := parts[0]
		value := parts[1]
		
		// Check if this is an RPC endpoint variable
		if strings.HasPrefix(key, "RPC_ENDPOINT_CHAIN_") {
			chainIDStr := strings.TrimPrefix(key, "RPC_ENDPOINT_CHAIN_")
			chainID, err := strconv.ParseInt(chainIDStr, 10, 64)
			if err != nil {
				continue
			}
			
			// Get or create network for this chain ID
			network, exists := networks[chainID]
			if !exists {
				network = Network{
					ID:       chainID,
					Name:     fmt.Sprintf("Chain %d", chainID),
					Explorer: fmt.Sprintf("https://explorer-chain-%d.com", chainID),
				}
			}
			network.RPCUrl = value
			networks[chainID] = network
		}
	}
	
	// Load network names from environment variables
	for _, envVar := range os.Environ() {
		parts := strings.Split(envVar, "=")
		if len(parts) != 2 {
			continue
		}
		
		key := parts[0]
		value := parts[1]
		
		if strings.HasPrefix(key, "NETWORK_NAME_CHAIN_") {
			chainIDStr := strings.TrimPrefix(key, "NETWORK_NAME_CHAIN_")
			chainID, err := strconv.ParseInt(chainIDStr, 10, 64)
			if err != nil {
				continue
			}
			
			if network, exists := networks[chainID]; exists {
				network.Name = value
				networks[chainID] = network
			}
		}
	}
	
	// Load explorer URLs from environment variables
	for _, envVar := range os.Environ() {
		parts := strings.Split(envVar, "=")
		if len(parts) != 2 {
			continue
		}
		
		key := parts[0]
		value := parts[1]
		
		if strings.HasPrefix(key, "EXPLORER_URL_CHAIN_") {
			chainIDStr := strings.TrimPrefix(key, "EXPLORER_URL_CHAIN_")
			chainID, err := strconv.ParseInt(chainIDStr, 10, 64)
			if err != nil {
				continue
			}
			
			if network, exists := networks[chainID]; exists {
				network.Explorer = value
				networks[chainID] = network
			}
		}
	}
	
	return networks
}

// InitializeNetworks initializes the SupportedNetworks from environment variables or defaults
func InitializeNetworks() {
	SupportedNetworks = LoadNetworksFromEnv()
}

// GetNetworkCount returns the number of configured networks
func GetNetworkCount() int {
	if SupportedNetworks == nil {
		InitializeNetworks()
	}
	return len(SupportedNetworks)
}

// ListNetworkIDs returns a slice of all configured network IDs
func ListNetworkIDs() []int64 {
	if SupportedNetworks == nil {
		InitializeNetworks()
	}
	
	var ids []int64
	for id := range SupportedNetworks {
		ids = append(ids, id)
	}
	return ids
}

// TransactionRequest represents the input request for transaction explanation
type TransactionRequest struct {
	TxHash    string `json:"tx_hash" validate:"required"`
	NetworkID int64  `json:"network_id" validate:"required"`
}

// RawTransactionData contains the raw blockchain data
type RawTransactionData struct {
	TxHash    string                 `json:"tx_hash"`
	NetworkID int64                  `json:"network_id"`
	Trace     map[string]interface{} `json:"trace"`
	Logs      []interface{}          `json:"logs"`
	Receipt   map[string]interface{} `json:"receipt"`
	Block     map[string]interface{} `json:"block"`
}

// Call represents a decoded contract method invocation
type Call struct {
	Contract    string                 `json:"contract"`
	Method      string                 `json:"method"`
	Arguments   map[string]interface{} `json:"arguments"`
	GasUsed     uint64                 `json:"gas_used"`
	Value       string                 `json:"value"`     // Wei amount
	CallType    string                 `json:"call_type"` // call, delegatecall, staticcall, etc.
	Success     bool                   `json:"success"`
	ErrorReason string                 `json:"error_reason,omitempty"`
	Depth       int                    `json:"depth"` // Call depth for nested calls
}

// Event represents a decoded emitted event
type Event struct {
	Contract    string                 `json:"contract"`
	Name        string                 `json:"name"`
	Parameters  map[string]interface{} `json:"parameters"`
	Topics      []string               `json:"topics"`
	Data        string                 `json:"data"`
	BlockNumber uint64                 `json:"block_number"`
	TxIndex     uint                   `json:"tx_index"`
	LogIndex    uint                   `json:"log_index"`
	Removed     bool                   `json:"removed"`
}

// TokenTransfer represents a token transfer (ERC20/ERC721/ERC1155)
type TokenTransfer struct {
	Type            string `json:"type"`     // ERC20, ERC721, ERC1155, ETH
	Contract        string `json:"contract"` // Empty for ETH
	From            string `json:"from"`
	To              string `json:"to"`
	Amount          string `json:"amount"`   // For ERC20 and ETH (raw hex amount)
	TokenID         string `json:"token_id"` // For ERC721/ERC1155
	Symbol          string `json:"symbol,omitempty"`
	Name            string `json:"name,omitempty"`
	Decimals        int    `json:"decimals,omitempty"`
	FormattedAmount string `json:"formatted_amount,omitempty"` // Human-readable amount (e.g. "43.94")
	AmountUSD       string `json:"amount_usd,omitempty"`       // USD value (e.g. "1.45")
}

// WalletEffect represents the effect on a specific wallet
type WalletEffect struct {
	Address   string          `json:"address"`
	NetChange string          `json:"net_change"` // Overall ETH change
	Transfers []TokenTransfer `json:"transfers"`  // All token transfers
	GasSpent  string          `json:"gas_spent"`  // Gas spent (for tx sender)
	NewNonce  uint64          `json:"new_nonce"`  // New nonce (for tx sender)
}

// ExplanationResult holds the final narrative and metadata
type ExplanationResult struct {
	TxHash      string                 `json:"tx_hash"`
	NetworkID   int64                  `json:"network_id"`
	Summary     string                 `json:"summary"`   // Human-readable description
	Effects     []WalletEffect         `json:"effects"`   // Effects on each wallet
	Transfers   []TokenTransfer        `json:"transfers"` // All transfers in the transaction
	GasUsed     uint64                 `json:"gas_used"`
	GasPrice    string                 `json:"gas_price"`
	TxFee       string                 `json:"tx_fee"`
	Status      string                 `json:"status"` // success, failed, reverted
	Timestamp   time.Time              `json:"timestamp"`
	BlockNumber uint64                 `json:"block_number"`
	Links       map[string]string      `json:"links"`           // Map of entity → URL
	Risks       []string               `json:"risks,omitempty"` // Potential risks or warnings
	Tags        []string               `json:"tags,omitempty"`  // Transaction categorization tags
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// DecodedData contains the processed transaction data
type DecodedData struct {
	TxHash    string  `json:"tx_hash"`
	NetworkID int64   `json:"network_id"`
	Calls     []Call  `json:"calls"`
	Events    []Event `json:"events"`
}

// ToolInput represents input to agent tools
type ToolInput struct {
	Data map[string]interface{} `json:"data"`
}

// ToolOutput represents output from agent tools
type ToolOutput struct {
	Result map[string]interface{} `json:"result"`
	Error  string                 `json:"error,omitempty"`
}

// IsValidNetwork checks if the network ID is supported
func IsValidNetwork(networkID int64) bool {
	if SupportedNetworks == nil {
		InitializeNetworks()
	}
	_, exists := SupportedNetworks[networkID]
	return exists
}

// GetNetwork returns network info for a given ID
func GetNetwork(networkID int64) (Network, bool) {
	if SupportedNetworks == nil {
		InitializeNetworks()
	}
	network, exists := SupportedNetworks[networkID]
	return network, exists
}

// ToJSON converts any struct to JSON string
func ToJSON(v interface{}) string {
	bytes, _ := json.Marshal(v)
	return string(bytes)
}