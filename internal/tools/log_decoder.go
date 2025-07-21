package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/txplain/txplain/internal/models"
	"github.com/txplain/txplain/internal/rpc"
)

// NewLogDecoder creates a new LogDecoder tool
func NewLogDecoder() *LogDecoder {
	return &LogDecoder{}
}

// NewLogDecoderWithRPC creates a LogDecoder with RPC capabilities
func NewLogDecoderWithRPC(rpcClient *rpc.Client) *LogDecoder {
	return &LogDecoder{
		rpcClient:         rpcClient,
		signatureResolver: rpc.NewSignatureResolver(rpcClient, true), // Enable 4byte API
	}
}

// LogDecoder decodes transaction logs into structured events using RPC introspection
type LogDecoder struct {
	rpcClient         *rpc.Client
	signatureResolver *rpc.SignatureResolver
}

// Name returns the tool name
func (t *LogDecoder) Name() string {
	return "log_decoder"
}

// Description returns the tool description
func (t *LogDecoder) Description() string {
	return "Decodes blockchain transaction logs into structured events and token transfers"
}

// Dependencies returns the tools this processor depends on (none for log decoder)
func (t *LogDecoder) Dependencies() []string {
	return []string{} // Log decoder is typically run first
}

// Process processes logs and adds decoded events to baggage
func (t *LogDecoder) Process(ctx context.Context, baggage map[string]interface{}) error {
	// Extract raw log data from baggage
	rawData, ok := baggage["raw_data"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("missing raw_data in baggage")
	}

	logsData, ok := rawData["logs"].([]interface{})
	if !ok || logsData == nil {
		// No logs, add empty events to baggage
		baggage["events"] = []models.Event{}
		return nil
	}

	networkID := int64(1) // Default to Ethereum
	if nid, ok := rawData["network_id"].(float64); ok {
		networkID = int64(nid)
	}

	// Set up RPC client and signature resolver if not already set
	if t.rpcClient == nil {
		var err error
		t.rpcClient, err = rpc.NewClient(networkID)
		if err != nil {
			return fmt.Errorf("failed to create RPC client: %w", err)
		}
		t.signatureResolver = rpc.NewSignatureResolver(t.rpcClient, true)
	}

	events, err := t.decodeLogsWithRPC(ctx, logsData, networkID)
	if err != nil {
		return fmt.Errorf("failed to decode logs: %w", err)
	}

	// Add decoded events to baggage
	baggage["events"] = events
	return nil
}

// Run executes the log decoding with enhanced RPC-based analysis
func (t *LogDecoder) Run(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
	// Extract log data from input
	rawData, ok := input["raw_data"].(map[string]interface{})
	if !ok {
		return nil, NewToolError("log_decoder", "missing raw_data in input", "MISSING_DATA")
	}

	logsData, ok := rawData["logs"].([]interface{})
	if !ok || logsData == nil {
		// No logs, return empty events
		return map[string]interface{}{
			"events": []models.Event{},
		}, nil
	}

	networkID := int64(1) // Default to Ethereum
	if nid, ok := rawData["network_id"].(float64); ok {
		networkID = int64(nid)
	}

	// Set up RPC client and signature resolver if not already set
	if t.rpcClient == nil {
		var err error
		t.rpcClient, err = rpc.NewClient(networkID)
		if err != nil {
			return nil, NewToolError("log_decoder", fmt.Sprintf("failed to create RPC client: %v", err), "RPC_ERROR")
		}
		t.signatureResolver = rpc.NewSignatureResolver(t.rpcClient, true)
	}

	events, err := t.decodeLogsWithRPC(ctx, logsData, networkID)
	if err != nil {
		return nil, NewToolError("log_decoder", fmt.Sprintf("failed to decode logs: %v", err), "DECODE_ERROR")
	}

	return map[string]interface{}{
		"events": events,
	}, nil
}

// decodeLogs processes log entries and extracts events
func (t *LogDecoder) decodeLogs(logs []interface{}, networkID int64) ([]models.Event, error) {
	var events []models.Event

	for _, logEntry := range logs {
		logMap, ok := logEntry.(map[string]interface{})
		if !ok {
			continue
		}

		event, err := t.decodeLog(logMap)
		if err != nil {
			// Skip invalid logs but continue processing
			continue
		}

		if event != nil {
			events = append(events, *event)
		}
	}

	return events, nil
}

// decodeLog decodes a single log entry
func (t *LogDecoder) decodeLog(log map[string]interface{}) (*models.Event, error) {
	event := &models.Event{}

	// Extract contract address
	if address, ok := log["address"].(string); ok {
		event.Contract = address
	}

	// Extract topics
	if topicsInterface, ok := log["topics"].([]interface{}); ok {
		for _, topic := range topicsInterface {
			if topicStr, ok := topic.(string); ok {
				event.Topics = append(event.Topics, topicStr)
			}
		}
	}

	// Extract data
	if data, ok := log["data"].(string); ok {
		event.Data = data
	}

	// Extract block number
	if blockNumber, ok := log["blockNumber"].(string); ok {
		if bn, err := t.hexToUint64(blockNumber); err == nil {
			event.BlockNumber = bn
		}
	}

	// Extract transaction index
	if txIndex, ok := log["transactionIndex"].(string); ok {
		if ti, err := t.hexToUint64(txIndex); err == nil {
			event.TxIndex = uint(ti)
		}
	}

	// Extract log index
	if logIndex, ok := log["logIndex"].(string); ok {
		if li, err := t.hexToUint64(logIndex); err == nil {
			event.LogIndex = uint(li)
		}
	}

	// Extract removed flag
	if removed, ok := log["removed"].(bool); ok {
		event.Removed = removed
	}

	// Decode event name and parameters based on topic signatures
	if len(event.Topics) > 0 {
		eventName, parameters := t.decodeEventFromTopics(event.Topics, event.Data)
		event.Name = eventName
		event.Parameters = parameters
	}

	return event, nil
}

// decodeEventFromTopics decodes event name and parameters from topics and data
func (t *LogDecoder) decodeEventFromTopics(topics []string, data string) (string, map[string]interface{}) {
	if len(topics) == 0 {
		return "", nil
	}

	// Map of common event signatures to names
	eventMap := map[string]string{
		"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef": "Transfer",
		"0x8c5be1e5ebec7d5bd14f71427d1e84f3dd0314c0f7b2291e5b200ac8c7c3b925": "Approval",
		"0x17307eab39ab6107e8899845ad3d59bd9653f200f220920489ca2b5937696c31": "ApprovalForAll",
		"0x4c209b5fc8ad50758f13e2e1088ba56a560dff690a1c6fef26394f4c03821c4f": "Mint",
		"0xcc16f5dbb4873280815c1ee09dbd06736cffcc184412cf7a71a0fdb75d397ca5": "Burn",
		"0x1c411e9a96e071241c2f21f7726b17ae89e3cab4c78be50e062b03a9fffbbad1": "Sync",
		"0xd78ad95fa46c994b6551d0da85fc275fe613ce37657fb8d5e3d130840159d822": "Swap",
		"0x4a39dc06d4c0dbc64b70af90fd698a233a518aa5d07e595d983b8c0526c8f7fb": "PairCreated",
	}

	eventSig := topics[0]
	eventName := eventMap[eventSig]
	if eventName == "" {
		eventName = eventSig // Use signature if name not known
	}

	parameters := make(map[string]interface{})
	
	// Parse parameters based on known event signatures
	switch eventName {
	case "Transfer":
		if len(topics) >= 3 {
			parameters["from"] = topics[1]
			parameters["to"] = topics[2]
			if data != "" && data != "0x" {
				parameters["value"] = data
			}
			// For ERC721, there might be a tokenId in topics[3]
			if len(topics) >= 4 {
				parameters["tokenId"] = topics[3]
			}
		}
	case "Approval":
		if len(topics) >= 3 {
			parameters["owner"] = topics[1]
			parameters["spender"] = topics[2]
			if data != "" && data != "0x" {
				parameters["value"] = data
			}
		}
	case "ApprovalForAll":
		if len(topics) >= 3 {
			parameters["owner"] = topics[1]
			parameters["operator"] = topics[2]
			if data != "" && data != "0x" {
				parameters["approved"] = data
			}
		}
	case "Swap":
		// Uniswap V2/V3 Swap event
		parameters["raw_data"] = data
		if len(topics) >= 2 {
			parameters["sender"] = topics[1]
		}
		if len(topics) >= 3 {
			parameters["to"] = topics[2]
		}
	case "Sync":
		// Uniswap V2 Sync event
		parameters["raw_data"] = data
	case "PairCreated":
		// Uniswap V2 PairCreated event
		if len(topics) >= 3 {
			parameters["token0"] = topics[1]
			parameters["token1"] = topics[2]
		}
		parameters["raw_data"] = data
	default:
		// For unknown events, store raw data
		parameters["raw_data"] = data
		for i, topic := range topics {
			parameters[fmt.Sprintf("topic_%d", i)] = topic
		}
	}

	return eventName, parameters
}

// hexToUint64 converts hex string to uint64
func (t *LogDecoder) hexToUint64(hex string) (uint64, error) {
	if hex == "" || hex == "0x" {
		return 0, nil
	}
	
	// Remove 0x prefix if present
	if strings.HasPrefix(hex, "0x") {
		hex = hex[2:]
	}
	
	return strconv.ParseUint(hex, 16, 64)
} 

// decodeLogsWithRPC processes log entries with RPC enhancements
func (t *LogDecoder) decodeLogsWithRPC(ctx context.Context, logs []interface{}, networkID int64) ([]models.Event, error) {
	var events []models.Event

	for _, logEntry := range logs {
		logMap, ok := logEntry.(map[string]interface{})
		if !ok {
			continue
		}

		event, err := t.decodeLogWithRPC(ctx, logMap)
		if err != nil {
			continue
		}

		if event != nil {
			events = append(events, *event)
		}
	}

	return events, nil
}

// decodeLogWithRPC decodes a single log entry with RPC enhancement
func (t *LogDecoder) decodeLogWithRPC(ctx context.Context, log map[string]interface{}) (*models.Event, error) {
	event := &models.Event{}

	// Extract contract address
	if address, ok := log["address"].(string); ok {
		event.Contract = address
	}

	// Extract topics
	if topicsInterface, ok := log["topics"].([]interface{}); ok {
		for _, topic := range topicsInterface {
			if topicStr, ok := topic.(string); ok {
				event.Topics = append(event.Topics, topicStr)
			}
		}
	}

	// Extract data
	if data, ok := log["data"].(string); ok {
		event.Data = data
	}

	// Extract block number, transaction index, log index, etc.
	if blockNumber, ok := log["blockNumber"].(string); ok {
		if bn, err := t.hexToUint64(blockNumber); err == nil {
			event.BlockNumber = bn
		}
	}

	if txIndex, ok := log["transactionIndex"].(string); ok {
		if ti, err := t.hexToUint64(txIndex); err == nil {
			event.TxIndex = uint(ti)
		}
	}

	if logIndex, ok := log["logIndex"].(string); ok {
		if li, err := t.hexToUint64(logIndex); err == nil {
			event.LogIndex = uint(li)
		}
	}

	if removed, ok := log["removed"].(bool); ok {
		event.Removed = removed
	}

	// Enhanced event decoding with RPC signature resolution
	if len(event.Topics) > 0 {
		eventName, parameters, err := t.decodeEventWithSignatureResolution(ctx, event.Topics, event.Data, event.Contract)
		if err != nil {
					// Fall back to basic decoding
		eventName, parameters = t.decodeEventFromTopics(event.Topics, event.Data)
		}
		event.Name = eventName
		event.Parameters = parameters
	}

	return event, nil
}

// decodeEventWithSignatureResolution uses RPC and signature resolution for event decoding
func (t *LogDecoder) decodeEventWithSignatureResolution(ctx context.Context, topics []string, data string, contractAddress string) (string, map[string]interface{}, error) {
	if len(topics) == 0 {
		return "", nil, fmt.Errorf("no topics")
	}

	// Resolve event signature using our enhanced resolver
	eventSig := topics[0]
	sigInfo, err := t.signatureResolver.ResolveEventSignature(ctx, eventSig)
	if err != nil {
		return eventSig, nil, err
	}

	eventName := sigInfo.Name
	if eventName == "unknown" {
		eventName = eventSig
	}

	parameters := make(map[string]interface{})
	parameters["signature"] = sigInfo.Signature

	// Enhanced parameter parsing based on known event signatures and contract info
	if t.rpcClient != nil && contractAddress != "" {
		// Get contract info for better parameter interpretation
		if contractInfo, err := t.rpcClient.GetContractInfo(ctx, contractAddress); err == nil {
			parameters["contract_type"] = contractInfo.Type
			parameters["contract_name"] = contractInfo.Name
			parameters["contract_symbol"] = contractInfo.Symbol
		}
	}

	// Parse parameters based on signature
	switch eventName {
	case "Transfer":
		t.parseTransferEvent(topics, data, parameters)
	case "Approval":
		t.parseApprovalEvent(topics, data, parameters)
	case "Swap":
		t.parseSwapEvent(topics, data, parameters)
	default:
		// Store raw data for unknown events
		parameters["raw_data"] = data
		for i, topic := range topics {
			parameters[fmt.Sprintf("topic_%d", i)] = topic
		}
	}

	return eventName, parameters, nil
}

// parseTransferEvent parses Transfer events with enhanced data
func (t *LogDecoder) parseTransferEvent(topics []string, data string, parameters map[string]interface{}) {
	if len(topics) >= 3 {
		parameters["from"] = topics[1]
		parameters["to"] = topics[2]
		if data != "" && data != "0x" {
			parameters["value"] = data
			// Try to parse as decimal if possible
			if len(data) == 66 { // 0x + 64 hex chars
				if value, err := strconv.ParseUint(data[2:], 16, 64); err == nil {
					parameters["value_decimal"] = value
				}
			}
		}
		// For ERC721, there might be a tokenId in topics[3]
		if len(topics) >= 4 {
			parameters["tokenId"] = topics[3]
		}
	}
}

// parseApprovalEvent parses Approval events
func (t *LogDecoder) parseApprovalEvent(topics []string, data string, parameters map[string]interface{}) {
	if len(topics) >= 3 {
		parameters["owner"] = topics[1]
		parameters["spender"] = topics[2]
		if data != "" && data != "0x" {
			parameters["value"] = data
		}
	}
}

// parseSwapEvent parses Swap events (Uniswap V2/V3)
func (t *LogDecoder) parseSwapEvent(topics []string, data string, parameters map[string]interface{}) {
	parameters["swap_detected"] = true
	parameters["raw_data"] = data
	if len(topics) >= 2 {
		parameters["sender"] = topics[1]
	}
	if len(topics) >= 3 {
		parameters["to"] = topics[2]
	}
} 