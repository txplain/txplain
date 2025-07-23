package tools

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/txplain/txplain/internal/models"
	"github.com/txplain/txplain/internal/rpc"
)

// TraceDecoder decodes transaction traces into structured calls using RPC introspection
type TraceDecoder struct {
	rpcClient         *rpc.Client
	signatureResolver *rpc.SignatureResolver
}

// NewTraceDecoder creates a new TraceDecoder tool
func NewTraceDecoder() *TraceDecoder {
	return &TraceDecoder{}
}

// NewTraceDecoderWithRPC creates a TraceDecoder with RPC capabilities
func NewTraceDecoderWithRPC(rpcClient *rpc.Client) *TraceDecoder {
	return &TraceDecoder{
		rpcClient:         rpcClient,
		signatureResolver: rpc.NewSignatureResolver(rpcClient, true), // Enable 4byte API
	}
}

// Name returns the tool name
func (t *TraceDecoder) Name() string {
	return "trace_decoder"
}

// Description returns the tool description
func (t *TraceDecoder) Description() string {
	return "Decodes blockchain transaction traces into structured function calls using RPC introspection and signature resolution"
}

// Dependencies returns the tools this processor depends on
func (t *TraceDecoder) Dependencies() []string {
	return []string{} // No dependencies - can run early in pipeline
}

// Process processes trace data from baggage and stores decoded calls
func (t *TraceDecoder) Process(ctx context.Context, baggage map[string]interface{}) error {
	// Extract raw data from baggage
	rawData, ok := baggage["raw_data"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("missing raw_data in baggage")
	}

	traceData, ok := rawData["trace"].(map[string]interface{})
	if !ok || traceData == nil {
		// No trace data available, set empty calls in baggage
		baggage["calls"] = []models.Call{}
		return nil
	}

	networkID := int64(1) // Default to Ethereum
	if nid, ok := rawData["network_id"].(float64); ok {
		networkID = int64(nid)
	}

	// Set up RPC client for this network if not already set
	if t.rpcClient == nil {
		var err error
		t.rpcClient, err = rpc.NewClient(networkID)
		if err != nil {
			return fmt.Errorf("failed to create RPC client: %w", err)
		}
		t.signatureResolver = rpc.NewSignatureResolver(t.rpcClient, true)
	}

	calls, err := t.decodeTrace(ctx, traceData, networkID)
	if err != nil {
		return fmt.Errorf("failed to decode trace: %w", err)
	}

	// Store calls in baggage for transaction explainer
	baggage["calls"] = calls
	return nil
}

// Run executes the trace decoding with enhanced RPC-based analysis
func (t *TraceDecoder) Run(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
	// Extract trace data from input
	rawData, ok := input["raw_data"].(map[string]interface{})
	if !ok {
		return nil, NewToolError("trace_decoder", "missing raw_data in input", "MISSING_DATA")
	}

	traceData, ok := rawData["trace"].(map[string]interface{})
	if !ok || traceData == nil {
		// No trace data available, return empty calls
		return map[string]interface{}{
			"calls": []models.Call{},
		}, nil
	}

	networkID := int64(1) // Default to Ethereum
	if nid, ok := rawData["network_id"].(float64); ok {
		networkID = int64(nid)
	}

	// Set up RPC client for this network if not already set
	if t.rpcClient == nil {
		var err error
		t.rpcClient, err = rpc.NewClient(networkID)
		if err != nil {
			return nil, NewToolError("trace_decoder", fmt.Sprintf("failed to create RPC client: %v", err), "RPC_ERROR")
		}
		t.signatureResolver = rpc.NewSignatureResolver(t.rpcClient, true)
	}

	calls, err := t.decodeTrace(ctx, traceData, networkID)
	if err != nil {
		return nil, NewToolError("trace_decoder", fmt.Sprintf("failed to decode trace: %v", err), "DECODE_ERROR")
	}

	return map[string]interface{}{
		"calls": calls,
	}, nil
}

// decodeTrace processes the trace data and extracts calls with RPC enhancement
func (t *TraceDecoder) decodeTrace(ctx context.Context, trace map[string]interface{}, networkID int64) ([]models.Call, error) {
	var calls []models.Call

	// Handle different trace formats based on network
	// Generic approach: detect trace format based on structure, not chain ID
	// This works with any network without hardcoding specific chain IDs
	if err := t.decodeTraceByStructure(ctx, trace, &calls); err != nil {
		return nil, fmt.Errorf("failed to decode trace for network %d: %w", networkID, err)
	}

	return calls, nil
}

// decodeTraceByStructure automatically detects and decodes trace format based on structure
// This generic approach works with any network without hardcoding chain IDs
func (t *TraceDecoder) decodeTraceByStructure(ctx context.Context, trace map[string]interface{}, calls *[]models.Call) error {
	// Try to detect format based on trace structure
	if _, hasType := trace["type"]; hasType {
		// This looks like a callTracer format (has "type" field)
		return t.decodeCallTracerResult(ctx, trace, calls, 0)
	} else if _, hasAction := trace["action"]; hasAction {
		// This looks like an Arbitrum trace format (has "action" field)
		return t.decodeArbitrumTrace(ctx, trace, calls)
	} else if traceArray, isArray := trace["result"].([]interface{}); isArray {
		// Handle array-based trace formats
		for _, item := range traceArray {
			if itemMap, ok := item.(map[string]interface{}); ok {
				if err := t.decodeTraceByStructure(ctx, itemMap, calls); err != nil {
					return err
				}
			}
		}
		return nil
	}

	// Fallback: try callTracer format first, then Arbitrum format
	err1 := t.decodeCallTracerResult(ctx, trace, calls, 0)
	if err1 == nil {
		return nil
	}

	err2 := t.decodeArbitrumTrace(ctx, trace, calls)
	if err2 == nil {
		return nil
	}

	return fmt.Errorf("unknown trace format (callTracer error: %v, arbitrum error: %v)", err1, err2)
}

// decodeCallTracerResult decodes call tracer format with focus on ETH transfers
func (t *TraceDecoder) decodeCallTracerResult(ctx context.Context, trace map[string]interface{}, calls *[]models.Call, depth int) error {
	// Only process calls that have meaningful data for explanations
	value, hasValue := trace["value"].(string)
	to, hasTo := trace["to"].(string)

	// Skip calls without ETH value or meaningful contract interaction
	if !hasValue || value == "" || value == "0x" || value == "0x0" {
		// Still process subcalls in case they have value
		if subCalls, ok := trace["calls"].([]interface{}); ok {
			for _, subCallInterface := range subCalls {
				if subCallMap, ok := subCallInterface.(map[string]interface{}); ok {
					if err := t.decodeCallTracerResult(ctx, subCallMap, calls, depth+1); err != nil {
						continue // Skip failed subcalls
					}
				}
			}
		}
		return nil
	}

	// Extract basic call information for meaningful calls only
	call := models.Call{
		Depth: depth,
		Value: value,
	}

	// Extract call type
	if callType, ok := trace["type"].(string); ok {
		call.CallType = callType
	}

	if hasTo {
		call.Contract = to
	}

	// Don't hardcode method names - let LLM interpret the call based on type and value
	// The call type and value provide enough context for interpretation

	// Check call success
	if error, hasError := trace["error"].(string); hasError && error != "" {
		call.Success = false
		call.ErrorReason = error
	} else {
		call.Success = true
	}

	// Only add if it has meaningful information for the explanation
	if call.Contract != "" || call.Method != "" || (call.Value != "" && call.Value != "0" && call.Value != "0x0") {
		*calls = append(*calls, call)
	}

	// Process subcalls recursively
	if subCalls, ok := trace["calls"].([]interface{}); ok {
		for _, subCallInterface := range subCalls {
			if subCallMap, ok := subCallInterface.(map[string]interface{}); ok {
				if err := t.decodeCallTracerResult(ctx, subCallMap, calls, depth+1); err != nil {
					continue // Skip failed subcalls
				}
			}
		}
	}

	return nil
}

// weiToEther converts wei string to ether string - helper method
func (t *TraceDecoder) weiToEther(weiStr string) string {
	if weiStr == "" || weiStr == "0x" || weiStr == "0x0" {
		return "0"
	}

	// Remove 0x prefix if present
	if strings.HasPrefix(weiStr, "0x") {
		weiStr = weiStr[2:]
	}

	// Convert to big int
	wei, success := new(big.Int).SetString(weiStr, 16)
	if !success {
		return "0"
	}

	// Convert to ether (divide by 10^18)
	ether := new(big.Float).SetInt(wei)
	ether.Quo(ether, new(big.Float).SetFloat64(1e18))

	return ether.String()
}

// decodeArbitrumTrace decodes Arbitrum-specific trace format
func (t *TraceDecoder) decodeArbitrumTrace(ctx context.Context, trace map[string]interface{}, calls *[]models.Call) error {
	// Arbitrum trace format implementation would go here
	// For now, return empty to avoid errors
	return nil
}

// parseArgumentsBasic provides generic argument parsing
func (t *TraceDecoder) parseArgumentsBasic(methodName, argData string, args map[string]interface{}) {
	// Generic parsing - extract standard 32-byte parameters
	if len(argData) >= 64 {
		// Most methods have address/amount patterns - parse generically
		paramCount := len(argData) / 64
		for i := 0; i < paramCount && i < 4; i++ { // Limit to first 4 params to avoid noise
			start := i * 64
			end := start + 64
			if end <= len(argData) {
				paramHex := "0x" + argData[start:end]
				args[fmt.Sprintf("param_%d", i)] = paramHex
			}
		}
	}
}

// hexToUint64 converts hex string to uint64
func (t *TraceDecoder) hexToUint64(hex string) (uint64, error) {
	if hex == "" || hex == "0x" {
		return 0, nil
	}

	if strings.HasPrefix(hex, "0x") {
		hex = hex[2:]
	}

	return strconv.ParseUint(hex, 16, 64)
}
