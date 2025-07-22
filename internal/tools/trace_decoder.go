package tools

import (
	"context"
	"fmt"
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

// decodeCallTracerResult decodes call tracer format with RPC enhancements
func (t *TraceDecoder) decodeCallTracerResult(ctx context.Context, trace map[string]interface{}, calls *[]models.Call, depth int) error {
	// Extract basic call information
	call := models.Call{
		Depth: depth,
	}

	// Extract contract address
	if to, ok := trace["to"].(string); ok {
		call.Contract = to
	}

	// Extract call type
	if callType, ok := trace["type"].(string); ok {
		call.CallType = callType
	}

	// Extract value
	if value, ok := trace["value"].(string); ok {
		call.Value = value
	}

	// Extract gas used
	if gasUsed, ok := trace["gasUsed"].(string); ok {
		if gas, err := t.hexToUint64(gasUsed); err == nil {
			call.GasUsed = gas
		}
	}

	// Extract input data and decode method with RPC enhancement
	if input, ok := trace["input"].(string); ok {
		method, args, err := t.decodeMethodCallWithRPC(ctx, input, call.Contract)
		if err != nil {
			// Fall back to basic decoding if RPC fails
			method, args = t.decodeMethodCallBasic(input)
		}
		call.Method = method
		call.Arguments = args
	}

	// Check if call was successful
	if errorStr, ok := trace["error"].(string); ok {
		call.Success = false
		call.ErrorReason = errorStr
	} else {
		call.Success = true
	}

	// Enhance call with contract information if we have RPC access
	if t.rpcClient != nil && call.Contract != "" {
		if contractInfo, err := t.rpcClient.GetContractInfo(ctx, call.Contract); err == nil {
			if call.Arguments == nil {
				call.Arguments = make(map[string]interface{})
			}
			call.Arguments["contract_type"] = contractInfo.Type
			call.Arguments["contract_name"] = contractInfo.Name
			call.Arguments["contract_symbol"] = contractInfo.Symbol
		}
	}

	// Add the call if it's meaningful
	if call.Contract != "" || call.Method != "" {
		*calls = append(*calls, call)
	}

	// Process nested calls
	if callsArray, ok := trace["calls"].([]interface{}); ok {
		for _, nestedCall := range callsArray {
			if nestedCallMap, ok := nestedCall.(map[string]interface{}); ok {
				if err := t.decodeCallTracerResult(ctx, nestedCallMap, calls, depth+1); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// decodeMethodCallWithRPC attempts to decode method signature using RPC and external APIs
func (t *TraceDecoder) decodeMethodCallWithRPC(ctx context.Context, input, contractAddress string) (string, map[string]interface{}, error) {
	if len(input) < 10 {
		return "", nil, fmt.Errorf("input too short")
	}

	// Extract method signature (first 4 bytes / 8 hex chars after 0x)
	methodSig := input[0:10]

	// Resolve signature using our enhanced resolver
	sigInfo, err := t.signatureResolver.ResolveFunctionSignature(ctx, methodSig)
	if err != nil {
		return methodSig, nil, err
	}

	methodName := sigInfo.Name
	if methodName == "unknown" {
		methodName = methodSig
	}

	// Parse arguments with enhanced logic
	args := make(map[string]interface{})
	if len(input) > 10 {
		argData := input[10:]
		args["raw_data"] = argData
		args["signature"] = sigInfo.Signature

		// Enhanced argument parsing based on known function signatures
		if err := t.parseArgumentsEnhanced(ctx, sigInfo.Signature, argData, contractAddress, args); err == nil {
			// Successfully parsed with enhanced logic
		} else {
			// Fall back to basic parsing for common methods
			t.parseArgumentsBasic(methodName, argData, args)
		}
	}

	return methodName, args, nil
}

// parseArgumentsEnhanced parses function arguments with full signature awareness
func (t *TraceDecoder) parseArgumentsEnhanced(ctx context.Context, signature, argData, contractAddress string, args map[string]interface{}) error {
	// This would implement full ABI decoding based on the signature
	// For now, we'll handle the most common cases

	switch {
	case strings.HasPrefix(signature, "transfer("):
		return t.parseTransferArguments(argData, args)
	case strings.HasPrefix(signature, "transferFrom("):
		return t.parseTransferFromArguments(argData, args)
	case strings.HasPrefix(signature, "approve("):
		return t.parseApprovalArguments(argData, args)
	case strings.Contains(signature, "swap"):
		return t.parseSwapArguments(signature, argData, args)
	case strings.Contains(signature, "addLiquidity"):
		return t.parseLiquidityArguments(signature, argData, args)
	}

	return fmt.Errorf("signature not supported for enhanced parsing")
}

// parseTransferArguments parses ERC20/ERC721 transfer arguments
func (t *TraceDecoder) parseTransferArguments(argData string, args map[string]interface{}) error {
	if len(argData) < 128 {
		return fmt.Errorf("insufficient data")
	}

	// address to (32 bytes)
	toAddress := "0x" + argData[24:64]
	args["to"] = toAddress

	// uint256 amount/tokenId (32 bytes)
	amountHex := argData[64:128]
	args["amount"] = "0x" + amountHex

	// Try to parse as decimal
	if amount, err := strconv.ParseUint(amountHex, 16, 64); err == nil {
		args["amount_decimal"] = amount
	}

	return nil
}

// parseTransferFromArguments parses transferFrom arguments
func (t *TraceDecoder) parseTransferFromArguments(argData string, args map[string]interface{}) error {
	if len(argData) < 192 {
		return fmt.Errorf("insufficient data")
	}

	// address from (32 bytes)
	fromAddress := "0x" + argData[24:64]
	args["from"] = fromAddress

	// address to (32 bytes)
	toAddress := "0x" + argData[88:128]
	args["to"] = toAddress

	// uint256 amount/tokenId (32 bytes)
	amountHex := argData[128:192]
	args["amount"] = "0x" + amountHex

	return nil
}

// parseApprovalArguments parses approve arguments
func (t *TraceDecoder) parseApprovalArguments(argData string, args map[string]interface{}) error {
	if len(argData) < 128 {
		return fmt.Errorf("insufficient data")
	}

	// address spender (32 bytes)
	spenderAddress := "0x" + argData[24:64]
	args["spender"] = spenderAddress

	// uint256 amount (32 bytes)
	amountHex := argData[64:128]
	args["amount"] = "0x" + amountHex

	return nil
}

// parseSwapArguments parses Uniswap-style swap arguments
func (t *TraceDecoder) parseSwapArguments(signature, argData string, args map[string]interface{}) error {
	// This would be expanded based on specific swap function signatures
	args["swap_type"] = "detected"

	// Basic parsing - would be enhanced based on specific signatures
	if len(argData) >= 64 {
		args["amount_in"] = "0x" + argData[0:64]
	}

	return nil
}

// parseLiquidityArguments parses addLiquidity/removeLiquidity arguments
func (t *TraceDecoder) parseLiquidityArguments(signature, argData string, args map[string]interface{}) error {
	args["liquidity_operation"] = true

	// Would be expanded based on specific function signatures
	return nil
}

// decodeMethodCallBasic provides fallback basic decoding (original implementation)
func (t *TraceDecoder) decodeMethodCallBasic(input string) (string, map[string]interface{}) {
	if len(input) < 10 {
		return "", nil
	}

	methodSig := input[0:10]

	// Use a minimal set of most common signatures
	basicMethodMap := map[string]string{
		"0xa9059cbb": "transfer",
		"0x23b872dd": "transferFrom",
		"0x095ea7b3": "approve",
		"0x7ff36ab5": "swapExactETHForTokens",
		"0x38ed1739": "swapExactTokensForTokens",
	}

	methodName := basicMethodMap[methodSig]
	if methodName == "" {
		methodName = methodSig
	}

	args := make(map[string]interface{})
	if len(input) > 10 {
		args["raw_data"] = input[10:]
	}

	return methodName, args
}

// decodeArbitrumTrace decodes Arbitrum-specific trace format
func (t *TraceDecoder) decodeArbitrumTrace(ctx context.Context, trace map[string]interface{}, calls *[]models.Call) error {
	// Arbitrum trace format implementation would go here
	// For now, return empty to avoid errors
	return nil
}

// parseArgumentsBasic provides basic argument parsing for common methods
func (t *TraceDecoder) parseArgumentsBasic(methodName, argData string, args map[string]interface{}) {
	switch methodName {
	case "transfer":
		if len(argData) >= 128 {
			args["to"] = "0x" + argData[24:64]
			args["amount"] = "0x" + argData[64:128]
		}
	case "approve":
		if len(argData) >= 128 {
			args["spender"] = "0x" + argData[24:64]
			args["amount"] = "0x" + argData[64:128]
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
