package tools

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

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

// Dependencies returns the tools this processor depends on
func (t *LogDecoder) Dependencies() []string {
	return []string{"abi_resolver"} // Use ABI resolver before decoding
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

	events, err := t.decodeLogsWithRPC(ctx, logsData, networkID, baggage)
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

	events, err := t.decodeLogsWithRPC(ctx, logsData, networkID, nil)
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

// decodeEventFromTopics decodes event name and parameters generically from topics and data
func (t *LogDecoder) decodeEventFromTopics(topics []string, data string) (string, map[string]interface{}) {
	if len(topics) == 0 {
		return "", nil
	}

	// Use the event signature hash as the event name - no hardcoded mappings
	eventSig := topics[0]
	eventName := eventSig // Always use signature hash, let ABI resolution provide proper names

	parameters := make(map[string]interface{})

	// Parse all events generically without hardcoded parameter names
	t.parseGenericEvent(topics, data, parameters)

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
func (t *LogDecoder) decodeLogsWithRPC(ctx context.Context, logs []interface{}, networkID int64, baggage map[string]interface{}) ([]models.Event, error) {
	var events []models.Event

	for _, logEntry := range logs {
		logMap, ok := logEntry.(map[string]interface{})
		if !ok {
			continue
		}

		event, err := t.decodeLogWithRPC(ctx, logMap, baggage)
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
func (t *LogDecoder) decodeLogWithRPC(ctx context.Context, log map[string]interface{}, baggage map[string]interface{}) (*models.Event, error) {
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
		eventName, parameters, err := t.decodeEventWithSignatureResolution(ctx, event.Topics, event.Data, event.Contract, baggage)
		if err != nil {
			// Fall back to basic decoding
			eventName, parameters = t.decodeEventFromTopics(event.Topics, event.Data)
		}
		event.Name = eventName
		event.Parameters = parameters
	}

	return event, nil
}

// decodeEventWithSignatureResolution uses resolved ABIs first, then falls back to RPC and signature resolution
func (t *LogDecoder) decodeEventWithSignatureResolution(ctx context.Context, topics []string, data string, contractAddress string, baggage map[string]interface{}) (string, map[string]interface{}, error) {
	if len(topics) == 0 {
		return "", nil, fmt.Errorf("no topics")
	}

	eventSig := topics[0]
	eventName := ""
	signature := ""
	var abiMethod *ABIMethod

	// First, try to resolve using ABI resolver data if available - THIS IS THE ROBUST PART
	if baggage != nil {
		if resolvedContracts, ok := baggage["resolved_contracts"].(map[string]*ContractInfo); ok {
			if contractInfo, exists := resolvedContracts[strings.ToLower(contractAddress)]; exists && contractInfo.IsVerified {
				// Look for matching event in parsed ABI
				for i, method := range contractInfo.ParsedABI {
					if method.Type == "event" && method.Hash == eventSig {
						eventName = method.Name
						signature = method.Signature
						abiMethod = &contractInfo.ParsedABI[i]
						break
					}
				}
			}
		}
	}

	// If not found in resolved ABIs, fall back to signature resolver
	if eventName == "" && t.signatureResolver != nil {
		sigInfo, err := t.signatureResolver.ResolveEventSignature(ctx, eventSig)
		if err == nil {
			eventName = sigInfo.Name
			signature = sigInfo.Signature
		}
	}

	// Final fallback
	if eventName == "" || eventName == "unknown" {
		eventName = eventSig
	}

	parameters := make(map[string]interface{})
	if signature != "" {
		parameters["signature"] = signature
	}

	// Enhanced parameter parsing based on known event signatures and contract info
	if t.rpcClient != nil && contractAddress != "" {
		// Get contract info for better parameter interpretation
		if contractInfo, err := t.rpcClient.GetContractInfo(ctx, contractAddress); err == nil {
			parameters["contract_type"] = contractInfo.Type
			parameters["contract_name"] = contractInfo.Name
			parameters["contract_symbol"] = contractInfo.Symbol
		}
	}

	// ROBUST ABI-BASED PARSING - This is the key enhancement
	if abiMethod != nil {
		// Use ABI-based parsing for maximum accuracy
		abiParams, err := t.parseEventWithABI(topics, data, abiMethod)
		if err == nil {
			// Merge ABI-parsed parameters with existing ones
			for key, value := range abiParams {
				parameters[key] = value
			}
		} else {
			// If ABI parsing fails, fall back to signature-based parsing
			t.parseEventBySignature(eventName, topics, data, parameters)
		}
	} else {
		// Use signature-based parsing as fallback
		t.parseEventBySignature(eventName, topics, data, parameters)
	}

	return eventName, parameters, nil
}

// parseEventBySignature handles all events generically without hardcoded names
func (t *LogDecoder) parseEventBySignature(eventName string, topics []string, data string, parameters map[string]interface{}) {
	// All events are now handled generically - no hardcoded event names
	t.parseGenericEvent(topics, data, parameters)
}

// parseEventWithABI parses event parameters using ABI specification for maximum accuracy
func (t *LogDecoder) parseEventWithABI(topics []string, data string, abiMethod *ABIMethod) (map[string]interface{}, error) {
	parameters := make(map[string]interface{})

	if len(topics) == 0 || abiMethod == nil {
		return parameters, fmt.Errorf("no topics or ABI method")
	}

	// topics[0] is the event signature hash
	// topics[1...] are indexed parameters
	// data contains non-indexed parameters

	topicIndex := 1 // Start from topics[1] (skip event signature)
	dataOffset := 0 // Offset into the data hex string (after 0x)

	// Clean and prepare data for parsing
	cleanData := strings.TrimPrefix(data, "0x")

	for _, input := range abiMethod.Inputs {
		if input.Indexed {
			// Parse indexed parameter from topics
			if topicIndex < len(topics) {
				value, err := t.parseABIParameter(input, topics[topicIndex], true)
				if err == nil {
					parameters[input.Name] = value
				} else {
					// Store raw topic if parsing fails
					parameters[input.Name] = topics[topicIndex]
				}
				topicIndex++
			}
		} else {
			// Parse non-indexed parameter from data
			value, bytesConsumed, err := t.parseABIParameterFromData(input, cleanData, dataOffset)
			if err == nil {
				parameters[input.Name] = value
				dataOffset += bytesConsumed
			} else {
				// If we can't parse further, store remaining data
				if dataOffset*2 < len(cleanData) {
					parameters[input.Name] = "0x" + cleanData[dataOffset*2:]
				}
				break
			}
		}
	}

	return parameters, nil
}

// parseABIParameter parses a single ABI parameter (for indexed parameters in topics)
func (t *LogDecoder) parseABIParameter(input ABIInput, value string, isIndexed bool) (interface{}, error) {
	// Handle different parameter types
	switch {
	case input.Type == "address":
		return t.cleanAddress(value), nil
	case strings.HasPrefix(input.Type, "uint"):
		return t.parseUintParameter(value, input.Type)
	case strings.HasPrefix(input.Type, "int"):
		return t.parseIntParameter(value, input.Type)
	case input.Type == "bool":
		return t.parseBoolParameter(value)
	case strings.HasPrefix(input.Type, "bytes"):
		if isIndexed && (input.Type == "bytes" || strings.HasSuffix(input.Type, "[]")) {
			// For indexed dynamic types, we get the hash
			return value, nil
		}
		return value, nil
	case input.Type == "string":
		if isIndexed {
			// For indexed strings, we get the hash
			return value, nil
		}
		return t.parseStringParameter(value)
	default:
		// For unknown types, return as-is
		return value, nil
	}
}

// parseABIParameterFromData parses a parameter from the data field (non-indexed)
func (t *LogDecoder) parseABIParameterFromData(input ABIInput, data string, offset int) (interface{}, int, error) {
	// Each parameter takes 32 bytes (64 hex chars) in the data field
	if offset*2+64 > len(data) {
		return nil, 0, fmt.Errorf("insufficient data")
	}

	paramData := data[offset*2 : offset*2+64]
	value, err := t.parseABIParameter(input, "0x"+paramData, false)
	return value, 32, err // Always consume 32 bytes
}

// parseUintParameter parses uint parameters
func (t *LogDecoder) parseUintParameter(value string, paramType string) (interface{}, error) {
	// Extract bit size (uint256 -> 256)
	if len(value) >= 2 && strings.HasPrefix(value, "0x") {
		// Try to parse as uint64 first for small numbers
		if parsed, err := strconv.ParseUint(value[2:], 16, 64); err == nil {
			return parsed, nil
		}
		// For larger numbers, return hex string
		return value, nil
	}
	return value, fmt.Errorf("invalid uint format")
}

// parseIntParameter parses int parameters
func (t *LogDecoder) parseIntParameter(value string, paramType string) (interface{}, error) {
	if len(value) >= 2 && strings.HasPrefix(value, "0x") {
		// Try to parse as int64 first for small numbers
		if parsed, err := strconv.ParseInt(value[2:], 16, 64); err == nil {
			return parsed, nil
		}
		// For larger numbers, return hex string
		return value, nil
	}
	return value, fmt.Errorf("invalid int format")
}

// parseBoolParameter parses bool parameters
func (t *LogDecoder) parseBoolParameter(value string) (interface{}, error) {
	if value == "0x0000000000000000000000000000000000000000000000000000000000000000" {
		return false, nil
	} else if value == "0x0000000000000000000000000000000000000000000000000000000000000001" {
		return true, nil
	}
	// For other values, check if it's non-zero
	if strings.HasPrefix(value, "0x") {
		parsed, err := strconv.ParseUint(value[2:], 16, 64)
		return parsed != 0, err
	}
	return value, fmt.Errorf("invalid bool format")
}

// parseStringParameter parses string parameters (from data field)
func (t *LogDecoder) parseStringParameter(value string) (interface{}, error) {
	// String parsing from data is complex, return hex for now
	return value, nil
}

// cleanAddress removes padding from address values
func (t *LogDecoder) cleanAddress(address string) string {
	if address == "" {
		return ""
	}

	// If it's a padded address (64 chars after 0x), extract the last 40 chars
	if strings.HasPrefix(address, "0x") && len(address) == 66 {
		return "0x" + address[26:] // Take last 40 characters
	}

	return address
}

// parseGenericEvent extracts all available parameters from any event generically
func (t *LogDecoder) parseGenericEvent(topics []string, data string, parameters map[string]interface{}) {
	// Store all topics with generic names (topic_0 is event signature, skip it)
	for i := 1; i < len(topics); i++ {
		paramName := fmt.Sprintf("param_%d", i)
		paramValue := topics[i]

		// Store original hex value
		parameters[paramName] = paramValue
		parameters[paramName+"_type"] = "indexed"

		// Add decoded formats for additional context
		t.addDecodedFormats(parameters, paramName, paramValue)
	}

	// Parse data field if present
	if data != "" && data != "0x" {
		dataLen := len(data) - 2 // Remove 0x prefix
		if dataLen >= 64 {
			// Each 32-byte chunk is a parameter
			paramIndex := len(topics) // Start numbering after indexed parameters
			for offset := 0; offset < dataLen; offset += 64 {
				if offset+64 <= dataLen {
					paramHex := "0x" + data[2+offset:2+offset+64]
					paramName := fmt.Sprintf("param_%d", paramIndex)

					// Store original hex value
					parameters[paramName] = paramHex
					parameters[paramName+"_type"] = "data"

					// Add decoded formats for additional context
					t.addDecodedFormats(parameters, paramName, paramHex)

					paramIndex++
				}
			}
		}
	}
}

// addDecodedFormats adds decimal, UTF-8, and address decoded versions of hex parameters
func (t *LogDecoder) addDecodedFormats(parameters map[string]interface{}, paramKey, hexValue string) {
	// Try to convert to decimal (for numeric values)
	if decimal := t.hexToDecimal(hexValue); decimal != "" {
		parameters[paramKey+"_decimal"] = decimal
	}

	// Try to convert to UTF-8 string (for string values)
	if utf8Str := t.hexToUTF8(hexValue); utf8Str != "" {
		parameters[paramKey+"_utf8"] = utf8Str
	}

	// For addresses, clean up the format
	if t.isLikelyAddress(hexValue) {
		parameters[paramKey+"_address"] = t.cleanAddressFormat(hexValue)
	}

	// For boolean-like values
	if t.isLikelyBoolean(hexValue) {
		parameters[paramKey+"_boolean"] = t.hexToBoolean(hexValue)
	}
}

// hexToDecimal converts hex string to decimal string (for likely numeric values)
func (t *LogDecoder) hexToDecimal(hexStr string) string {
	// Remove 0x prefix
	cleanHex := strings.TrimPrefix(hexStr, "0x")
	if len(cleanHex) == 0 {
		return ""
	}

	// Convert to big.Int to handle large numbers
	bigInt := new(big.Int)
	if _, ok := bigInt.SetString(cleanHex, 16); !ok {
		return ""
	}

	// Only include decimal if it's non-zero or exactly zero
	if bigInt.Cmp(big.NewInt(0)) == 0 {
		return "0"
	}

	decimalStr := bigInt.String()

	// For very large numbers, add a hint
	if len(decimalStr) > 15 {
		return decimalStr + " (large_number)"
	}

	return decimalStr
}

// hexToUTF8 attempts to convert hex to UTF-8 string if it contains readable text
func (t *LogDecoder) hexToUTF8(hexStr string) string {
	// Remove 0x prefix
	cleanHex := strings.TrimPrefix(hexStr, "0x")
	if len(cleanHex) == 0 || len(cleanHex)%2 != 0 {
		return ""
	}

	// Convert hex to bytes
	bytes, err := hex.DecodeString(cleanHex)
	if err != nil {
		return ""
	}

	// Remove null bytes (common in padded strings)
	if lastNonZero := t.findLastNonZero(bytes); lastNonZero >= 0 {
		bytes = bytes[:lastNonZero+1]
	} else {
		return "" // All zeros
	}

	// Check if it's valid UTF-8 and contains mostly printable characters
	if !utf8.Valid(bytes) {
		return ""
	}

	str := string(bytes)

	// Only return if it contains mostly printable ASCII/UTF-8 characters and is meaningful
	if t.isPrintableString(str) && len(str) >= 2 {
		return str
	}

	return ""
}

// isLikelyAddress checks if hex value looks like an Ethereum address
func (t *LogDecoder) isLikelyAddress(hexStr string) bool {
	cleanHex := strings.TrimPrefix(hexStr, "0x")

	// Ethereum addresses are 40 hex characters, but can be padded to 64
	if len(cleanHex) == 64 {
		// Check if first 24 characters are zeros (padded address)
		return cleanHex[:24] == "000000000000000000000000"
	}

	return len(cleanHex) == 40
}

// isLikelyBoolean checks if hex value represents a boolean (0 or 1)
func (t *LogDecoder) isLikelyBoolean(hexStr string) bool {
	cleanHex := strings.TrimPrefix(hexStr, "0x")

	// Remove leading zeros
	cleanHex = strings.TrimLeft(cleanHex, "0")

	return cleanHex == "" || cleanHex == "1" // empty means all zeros (false), "1" means true
}

// hexToBoolean converts hex to boolean value
func (t *LogDecoder) hexToBoolean(hexStr string) bool {
	cleanHex := strings.TrimPrefix(hexStr, "0x")
	cleanHex = strings.TrimLeft(cleanHex, "0")

	return cleanHex == "1"
}

// cleanAddressFormat extracts clean address from padded hex
func (t *LogDecoder) cleanAddressFormat(hexStr string) string {
	cleanHex := strings.TrimPrefix(hexStr, "0x")

	if len(cleanHex) == 64 && cleanHex[:24] == "000000000000000000000000" {
		return "0x" + cleanHex[24:]
	}

	if len(cleanHex) == 40 {
		return "0x" + cleanHex
	}

	return hexStr
}

// findLastNonZero finds the index of the last non-zero byte
func (t *LogDecoder) findLastNonZero(bytes []byte) int {
	for i := len(bytes) - 1; i >= 0; i-- {
		if bytes[i] != 0 {
			return i
		}
	}
	return -1
}

// isPrintableString checks if string contains mostly printable characters
func (t *LogDecoder) isPrintableString(s string) bool {
	if len(s) == 0 {
		return false
	}

	printableCount := 0
	for _, r := range s {
		if unicode.IsPrint(r) || unicode.IsSpace(r) {
			printableCount++
		}
	}

	// At least 80% of characters should be printable
	return float64(printableCount)/float64(len([]rune(s))) >= 0.8
}
