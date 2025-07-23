package models

import (
	"context"
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

// Annotation represents an interactive element in the explanation text
type Annotation struct {
	Text    string `json:"text"`              // Text to match (e.g. "0@100 USDT" for first occurrence)
	Link    string `json:"link,omitempty"`    // Optional URL to link to
	Tooltip string `json:"tooltip,omitempty"` // Optional HTML tooltip content
	Icon    string `json:"icon,omitempty"`    // Optional icon URL or path
}

// AnnotationContextItem represents a piece of context that can be used for annotations
type AnnotationContextItem struct {
	Type        string                 `json:"type"`                  // token, address, protocol, amount, etc.
	Value       string                 `json:"value"`                 // The actual value (address, token symbol, etc.)
	Name        string                 `json:"name,omitempty"`        // Human-readable name
	Icon        string                 `json:"icon,omitempty"`        // Icon URL or path
	Link        string                 `json:"link,omitempty"`        // Link URL
	Description string                 `json:"description,omitempty"` // Description for tooltip
	Metadata    map[string]interface{} `json:"metadata,omitempty"`    // Additional metadata
}

// AnnotationContext holds all context data that tools contribute for annotations
type AnnotationContext struct {
	Items []AnnotationContextItem `json:"items"`
}

// Add method to append context items
func (ac *AnnotationContext) AddItem(item AnnotationContextItem) {
	ac.Items = append(ac.Items, item)
}

// Add method to add token context
func (ac *AnnotationContext) AddToken(address, symbol, name, icon, description string, metadata map[string]interface{}) {
	ac.AddItem(AnnotationContextItem{
		Type:        "token",
		Value:       address,
		Name:        fmt.Sprintf("%s (%s)", name, symbol),
		Icon:        icon,
		Description: description,
		Metadata:    metadata,
	})
}

// Add method to add address context
func (ac *AnnotationContext) AddAddress(address, name, link, description string) {
	ac.AddItem(AnnotationContextItem{
		Type:        "address",
		Value:       address,
		Name:        name,
		Link:        link,
		Description: description,
	})
}

// Add method to add protocol context
func (ac *AnnotationContext) AddProtocol(name, icon, link, description string, metadata map[string]interface{}) {
	ac.AddItem(AnnotationContextItem{
		Type:        "protocol",
		Value:       name,
		Name:        name,
		Icon:        icon,
		Link:        link,
		Description: description,
		Metadata:    metadata,
	})
}

// Add method to add amount context
func (ac *AnnotationContext) AddAmount(amount, symbol, usdValue, description string) {
	ac.AddItem(AnnotationContextItem{
		Type:        "amount",
		Value:       fmt.Sprintf("%s %s", amount, symbol),
		Description: description,
		Metadata: map[string]interface{}{
			"amount":    amount,
			"symbol":    symbol,
			"usd_value": usdValue,
		},
	})
}

// AddressParticipant represents an address involved in the transaction with its role and metadata
type AddressParticipant struct {
	Address     string                 `json:"address"`
	Role        string                 `json:"role"`                  // e.g., "Token Trader", "DEX Router", "Token Contract (USDT)"
	Category    string                 `json:"category"`              // e.g., "user", "protocol", "token"
	Type        string                 `json:"type"`                  // "EOA" or "Contract"
	ENSName     string                 `json:"ens_name,omitempty"`    // ENS name if available
	Name        string                 `json:"name,omitempty"`        // Human-readable name (from token metadata, protocol names, etc.)
	Icon        string                 `json:"icon,omitempty"`        // Icon URL if available
	Link        string                 `json:"link,omitempty"`        // Explorer link
	Description string                 `json:"description,omitempty"` // Additional context
	Metadata    map[string]interface{} `json:"metadata,omitempty"`    // Additional data
}

// ExplanationResult holds the final narrative and metadata
type ExplanationResult struct {
	TxHash       string                 `json:"tx_hash"`
	NetworkID    int64                  `json:"network_id"`
	Summary      string                 `json:"summary"`      // Human-readable description
	Participants []AddressParticipant   `json:"participants"` // All addresses involved with their roles
	Transfers    []TokenTransfer        `json:"transfers"`    // All transfers in the transaction
	GasUsed      uint64                 `json:"gas_used"`
	GasPrice     string                 `json:"gas_price"`
	Status       string                 `json:"status"` // success, failed, reverted
	Timestamp    time.Time              `json:"timestamp"`
	BlockNumber  uint64                 `json:"block_number"`
	Links        map[string]string      `json:"links"`           // Map of entity → URL (kept for backward compatibility)
	Risks        []string               `json:"risks,omitempty"` // Potential risks or warnings
	Tags         []string               `json:"tags,omitempty"`  // Transaction categorization tags
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
	Annotations  []Annotation           `json:"annotations,omitempty"` // Interactive annotations for the UI
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

// Progress models for real-time updates

// ComponentStatus represents the current status of a processing component
type ComponentStatus string

const (
	ComponentStatusInitiated ComponentStatus = "initiated"
	ComponentStatusRunning   ComponentStatus = "running"
	ComponentStatusFinished  ComponentStatus = "finished"
	ComponentStatusError     ComponentStatus = "error"
)

// ComponentGroup represents different processing phases with associated colors
type ComponentGroup string

const (
	ComponentGroupData       ComponentGroup = "data"        // Blue - Data fetching
	ComponentGroupDecoding   ComponentGroup = "decoding"    // Green - Decoding/parsing
	ComponentGroupEnrichment ComponentGroup = "enrichment"  // Purple - Data enrichment
	ComponentGroupAnalysis   ComponentGroup = "analysis"    // Orange - AI analysis
	ComponentGroupFinishing  ComponentGroup = "finishing"   // Gray - Final steps
)

// ComponentUpdate represents a single component's progress update
type ComponentUpdate struct {
	ID          string          `json:"id"`          // Unique component ID
	Group       ComponentGroup  `json:"group"`       // Visual grouping
	Title       string          `json:"title"`       // Display title
	Status      ComponentStatus `json:"status"`      // Current status
	Description string          `json:"description"` // Optional detailed description
	Timestamp   time.Time       `json:"timestamp"`   // When this update occurred
	StartTime   *time.Time      `json:"start_time"`  // When this component started (always included)
	Duration    int64           `json:"duration_ms"` // Duration in milliseconds (0 for running components)
	Metadata    interface{}     `json:"metadata,omitempty"` // Optional tool-specific data
}

// ProgressEvent represents a complete progress event sent via SSE
type ProgressEvent struct {
	Type      string           `json:"type"`      // "component_update", "complete", "error"
	Component *ComponentUpdate `json:"component,omitempty"` // For component updates
	Result    interface{}      `json:"result,omitempty"`    // For completion
	Error     string           `json:"error,omitempty"`     // For errors
	Timestamp time.Time        `json:"timestamp"`
}

// ProgressTracker tracks all component updates for a transaction
type ProgressTracker struct {
	components  map[string]*ComponentUpdate
	updateChan  chan<- ProgressEvent
	lastUpdate  time.Time
	heartbeatID string
	startTimes  map[string]time.Time // Track when each component started
	done        chan struct{}
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewProgressTracker creates a new progress tracker
func NewProgressTracker(updateChan chan<- ProgressEvent) *ProgressTracker {
	ctx, cancel := context.WithCancel(context.Background())
	
	pt := &ProgressTracker{
		components:  make(map[string]*ComponentUpdate),
		updateChan:  updateChan,
		lastUpdate:  time.Now(),
		heartbeatID: "heartbeat",
		startTimes:  make(map[string]time.Time),
		done:        make(chan struct{}),
		ctx:         ctx,
		cancel:      cancel,
	}
	
	// Start heartbeat goroutine to ensure updates every 500ms
	go pt.startHeartbeat()
	
	return pt
}

// Close stops the heartbeat goroutine and cleans up resources
func (pt *ProgressTracker) Close() {
	pt.cancel()
	close(pt.done)
}

// UpdateComponent updates a component's status and sends progress event
func (pt *ProgressTracker) UpdateComponent(id string, group ComponentGroup, title string, status ComponentStatus, description string) {
	now := time.Now()
	
	// Track start time for new components
	var startTime *time.Time
	var duration int64
	
	if _, exists := pt.startTimes[id]; !exists {
		// This is the first time we see this component, record start time
		pt.startTimes[id] = now
		startTime = &now
		duration = 0 // No duration yet for new components
	} else {
		// Component already exists, use existing start time
		existingStart := pt.startTimes[id]
		startTime = &existingStart
		
		// Calculate duration for all components based on elapsed time
		duration = now.Sub(existingStart).Milliseconds()
		
		// Ensure minimum duration of 1ms for any component that has been running
		// to avoid displaying "Starting..." for components that have actually started
		if duration == 0 && (status == ComponentStatusRunning || status == ComponentStatusFinished || status == ComponentStatusError) {
			duration = 1
		}
	}
	
	component := &ComponentUpdate{
		ID:          id,
		Group:       group,
		Title:       title,
		Status:      status,
		Description: description,
		Timestamp:   now,
		StartTime:   startTime,
		Duration:    duration,
	}
	
	pt.components[id] = component
	pt.lastUpdate = now

	// Send update via channel with panic protection
	select {
	case <-pt.ctx.Done():
		// Tracker has been closed, don't send
		return
	default:
		// Try to send, but recover from panic if channel is closed
		func() {
			defer func() {
				if recover() != nil {
					// Channel was closed, cancel context to stop further attempts
					pt.cancel()
				}
			}()
			
			if pt.updateChan != nil {
				pt.updateChan <- ProgressEvent{
					Type:      "component_update",
					Component: component,
					Timestamp: time.Now(),
				}
			}
		}()
	}
}

// startHeartbeat ensures there's always a progress update within 500ms
func (pt *ProgressTracker) startHeartbeat() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	
	for {
		select {
		case <-pt.ctx.Done():
			// Context cancelled, stop heartbeat
			return
		case <-pt.done:
			// Explicit done signal, stop heartbeat
			return
		case <-ticker.C:
			// Check if we need to send a heartbeat
			if time.Since(pt.lastUpdate) >= 450*time.Millisecond {
				// Find the most recent running component to update
				var latestRunning *ComponentUpdate
				var latestTime time.Time
				
				for _, component := range pt.components {
					if component.Status == ComponentStatusRunning && component.Timestamp.After(latestTime) {
						latestRunning = component
						latestTime = component.Timestamp
					}
				}
				
				if latestRunning != nil {
					// Send a subtle heartbeat update
					heartbeatDesc := latestRunning.Description
					if !strings.Contains(heartbeatDesc, "...") {
						heartbeatDesc += "..."
					}
					
					pt.UpdateComponent(
						latestRunning.ID,
						latestRunning.Group,
						latestRunning.Title,
						ComponentStatusRunning,
						heartbeatDesc,
					)
				}
			}
		}
	}
}

// GetAllComponents returns all component updates
func (pt *ProgressTracker) GetAllComponents() []*ComponentUpdate {
	var components []*ComponentUpdate
	for _, component := range pt.components {
		components = append(components, component)
	}
	return components
}

// SendComplete sends completion event
func (pt *ProgressTracker) SendComplete(result interface{}) {
	select {
	case <-pt.ctx.Done():
		// Tracker has been closed, don't send
		return
	default:
		// Try to send, but recover from panic if channel is closed
		func() {
			defer func() {
				if recover() != nil {
					// Channel was closed, cancel context to stop further attempts
					pt.cancel()
				}
			}()
			
			if pt.updateChan != nil {
				pt.updateChan <- ProgressEvent{
					Type:      "complete",
					Result:    result,
					Timestamp: time.Now(),
				}
			}
		}()
	}
}

// SendError sends error event
func (pt *ProgressTracker) SendError(err error) {
	select {
	case <-pt.ctx.Done():
		// Tracker has been closed, don't send
		return
	default:
		// Try to send, but recover from panic if channel is closed
		func() {
			defer func() {
				if recover() != nil {
					// Channel was closed, cancel context to stop further attempts
					pt.cancel()
				}
			}()
			
			if pt.updateChan != nil {
				pt.updateChan <- ProgressEvent{
					Type:      "error",
					Error:     err.Error(),
					Timestamp: time.Now(),
				}
			}
		}()
	}
}
