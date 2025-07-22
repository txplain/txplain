package rpc

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/txplain/txplain/internal/models"
	"golang.org/x/crypto/sha3"
)

type Client struct {
	httpClient *http.Client
	network    models.Network
}

type JSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
	ID      int         `json:"id"`
}

type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result"`
	Error   *JSONRPCError   `json:"error"`
	ID      int             `json:"id"`
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

// NewClient creates a new RPC client for the specified network
func NewClient(networkID int64) (*Client, error) {
	network, exists := models.GetNetwork(networkID)
	if !exists {
		return nil, fmt.Errorf("unsupported network ID: %d", networkID)
	}

	return &Client{
		httpClient: &http.Client{
			Timeout: 120 * time.Second, // Increased for complex transactions
		},
		network: network,
	}, nil
}

// call makes a JSON-RPC call to the blockchain node
func (c *Client) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      1,
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.network.RPCUrl, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Debug: print raw response if it's not JSON
	if len(body) > 0 && body[0] != '{' && body[0] != '[' {
		fmt.Printf("DEBUG: Non-JSON response from %s: %s\n", c.network.RPCUrl, string(body))
	}

	var rpcResp JSONRPCResponse
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		bodyPreview := string(body)
		if len(bodyPreview) > 200 {
			bodyPreview = bodyPreview[:200]
		}
		return nil, fmt.Errorf("failed to unmarshal response: %w (body: %s)", err, bodyPreview)
	}

	if rpcResp.Error != nil {
		return nil, fmt.Errorf("RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return rpcResp.Result, nil
}

// GetTransactionByHash retrieves transaction data by hash
func (c *Client) GetTransactionByHash(ctx context.Context, txHash string) (map[string]interface{}, error) {
	result, err := c.call(ctx, "eth_getTransactionByHash", []string{txHash})
	if err != nil {
		return nil, err
	}

	var tx map[string]interface{}
	if err := json.Unmarshal(result, &tx); err != nil {
		return nil, fmt.Errorf("failed to unmarshal transaction: %w", err)
	}

	return tx, nil
}

// GetTransactionReceipt retrieves transaction receipt by hash
func (c *Client) GetTransactionReceipt(ctx context.Context, txHash string) (map[string]interface{}, error) {
	result, err := c.call(ctx, "eth_getTransactionReceipt", []string{txHash})
	if err != nil {
		return nil, err
	}

	var receipt map[string]interface{}
	if err := json.Unmarshal(result, &receipt); err != nil {
		return nil, fmt.Errorf("failed to unmarshal receipt: %w", err)
	}

	return receipt, nil
}

// GetBlockByNumber retrieves block data by number
func (c *Client) GetBlockByNumber(ctx context.Context, blockNumber string) (map[string]interface{}, error) {
	result, err := c.call(ctx, "eth_getBlockByNumber", []interface{}{blockNumber, false})
	if err != nil {
		return nil, err
	}

	var block map[string]interface{}
	if err := json.Unmarshal(result, &block); err != nil {
		return nil, fmt.Errorf("failed to unmarshal block: %w", err)
	}

	return block, nil
}

// TraceTransaction retrieves transaction trace (for supported networks)
func (c *Client) TraceTransaction(ctx context.Context, txHash string) (map[string]interface{}, error) {
	// Different networks may have different trace methods
	var method string
	var params interface{}

	// Generic approach: try different trace methods based on RPC capability detection
	// This works for any network without hardcoding specific chain IDs
	method, params, err := c.detectTraceMethod(txHash)
	if err != nil {
		return nil, fmt.Errorf("could not determine trace method for network %d: %w", c.network.ID, err)
	}

	result, err := c.call(ctx, method, params)
	if err != nil {
		return nil, err
	}

	var trace map[string]interface{}
	if err := json.Unmarshal(result, &trace); err != nil {
		return nil, fmt.Errorf("failed to unmarshal trace: %w", err)
	}

	return trace, nil
}

// detectTraceMethod dynamically detects the available trace method for the current network
// This generic approach works with any RPC without hardcoding specific chain IDs
func (c *Client) detectTraceMethod(txHash string) (string, interface{}, error) {
	ctx := context.Background()
	
	// Try standard debug_traceTransaction first (most common)
	testMethod := "debug_traceTransaction"
	testParams := []interface{}{txHash, map[string]interface{}{
		"tracer": "callTracer",
	}}
	
	// Test if the method is available by making a lightweight call
	// If it fails, we'll try alternative methods
	_, err := c.call(ctx, testMethod, testParams)
	if err == nil {
		return testMethod, testParams, nil
	}
	
	// Fallback: Try arbtrace_transaction (used by some L2s like Arbitrum)
	testMethod = "arbtrace_transaction" 
	testParams2 := []string{txHash}
	
	_, err2 := c.call(ctx, testMethod, testParams2)
	if err2 == nil {
		return testMethod, testParams2, nil
	}
	
	// If both methods fail, return the more standard one with original error
	// This allows graceful degradation - the calling code can handle the error
	return "debug_traceTransaction", []interface{}{txHash, map[string]interface{}{
		"tracer": "callTracer",
	}}, fmt.Errorf("trace methods not available (debug_traceTransaction: %v, arbtrace_transaction: %v)", err, err2)
}

// FetchTransactionData retrieves all relevant data for a transaction
func (c *Client) FetchTransactionData(ctx context.Context, txHash string) (*models.RawTransactionData, error) {
	// Fetch transaction, receipt, and trace in parallel
	txChan := make(chan map[string]interface{})
	receiptChan := make(chan map[string]interface{})
	traceChan := make(chan map[string]interface{})
	errChan := make(chan error, 3)

	// Get transaction
	go func() {
		tx, err := c.GetTransactionByHash(ctx, txHash)
		if err != nil {
			errChan <- err
			return
		}
		txChan <- tx
	}()

	// Get receipt
	go func() {
		receipt, err := c.GetTransactionReceipt(ctx, txHash)
		if err != nil {
			errChan <- err
			return
		}
		receiptChan <- receipt
	}()

	// Get trace
	go func() {
		trace, err := c.TraceTransaction(ctx, txHash)
		if err != nil {
			// Trace might not be available, so we'll continue without it
			traceChan <- nil
			return
		}
		traceChan <- trace
	}()

	var tx, receipt, trace map[string]interface{}
	var block map[string]interface{}

	// Collect results
	for i := 0; i < 3; i++ {
		select {
		case tx = <-txChan:
		case receipt = <-receiptChan:
		case trace = <-traceChan:
		case err := <-errChan:
			return nil, fmt.Errorf("failed to fetch transaction data: %w", err)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Get block information
	if tx != nil {
		if blockNumHex, ok := tx["blockNumber"].(string); ok {
			var err error
			block, err = c.GetBlockByNumber(ctx, blockNumHex)
			if err != nil {
				// Block fetch is not critical, continue without it
				block = nil
			}
		}
	}

	// Extract logs from receipt
	var logs []interface{}
	if receipt != nil {
		if receiptLogs, ok := receipt["logs"].([]interface{}); ok {
			logs = receiptLogs
		}
	}

	return &models.RawTransactionData{
		TxHash:    txHash,
		NetworkID: c.network.ID,
		Trace:     trace,
		Logs:      logs,
		Receipt:   receipt,
		Block:     block,
	}, nil
}

// GetNetwork returns the network information for this client
func (c *Client) GetNetwork() models.Network {
	return c.network
}

// ResolveENSName resolves an ENS name from an Ethereum address (reverse lookup)
func (c *Client) ResolveENSName(ctx context.Context, address string) (string, error) {
	// Only resolve on Ethereum mainnet
	if c.network.ID != 1 {
		return "", nil
	}

	// Clean the address
	if len(address) != 42 || address[:2] != "0x" {
		return "", fmt.Errorf("invalid address format")
	}

	// Remove 0x prefix and convert to lowercase
	addr := address[2:]
	addr = fmt.Sprintf("%040s", addr) // Ensure it's 40 characters

	// Create the ENS reverse lookup domain
	// For address 0x5c0a3834648c766dfa1c06b62520f222a4cd89a0
	// We create: 5c0a3834648c766dfa1c06b62520f222a4cd89a0.addr.reverse
	ensReverseDomain := fmt.Sprintf("%s.addr.reverse", strings.ToLower(addr))

	// ENS Registry address on mainnet
	ensRegistryAddress := "0x00000000000C2E074eC69A0dFb2997BA6C7d2e1e"

	// resolver(bytes32) function signature
	resolverSig := "0x0178b8bf"

	// Calculate the namehash of the reverse domain
	nameHash := c.namehash(ensReverseDomain)

	// Call ENS registry to get resolver
	params := []interface{}{
		map[string]interface{}{
			"to":   ensRegistryAddress,
			"data": resolverSig + nameHash[2:], // Remove 0x from namehash
		},
		"latest",
	}

	result, err := c.call(ctx, "eth_call", params)
	if err != nil {
		return "", err
	}

	var resolverResult string
	if err := json.Unmarshal(result, &resolverResult); err != nil {
		return "", err
	}

	// If no resolver, return empty
	if resolverResult == "0x" || len(resolverResult) < 66 {
		return "", nil
	}

	// Extract resolver address (last 40 characters)
	resolverAddress := "0x" + resolverResult[len(resolverResult)-40:]

	// If resolver is zero address, no ENS name
	if resolverAddress == "0x0000000000000000000000000000000000000000" {
		return "", nil
	}

	// Call resolver.name(bytes32) to get the actual name
	nameSig := "0x691f3431"
	nameParams := []interface{}{
		map[string]interface{}{
			"to":   resolverAddress,
			"data": nameSig + nameHash[2:],
		},
		"latest",
	}

	nameResult, err := c.call(ctx, "eth_call", nameParams)
	if err != nil {
		return "", err
	}

	var nameResultHex string
	if err := json.Unmarshal(nameResult, &nameResultHex); err != nil {
		return "", err
	}

	// Decode the name from the result
	if nameResultHex == "0x" || len(nameResultHex) < 130 {
		return "", nil
	}

	// Parse ABI encoded string
	ensName, err := c.decodeStringResult(nameResultHex)
	if err != nil {
		return "", err
	}

	// Skip validation for now since resolveENSForward is not implemented
	// In a production system, you'd want to validate reverse/forward consistency
	return ensName, nil
}

// namehash implements the ENS namehash algorithm
func (c *Client) namehash(name string) string {
	if name == "" {
		return "0x0000000000000000000000000000000000000000000000000000000000000000"
	}

	// Start with 32 zero bytes
	node := make([]byte, 32)

	// Split the name into labels (e.g., "vitalik.eth" -> ["vitalik", "eth"])
	labels := strings.Split(name, ".")

	// Process labels in reverse order (from right to left)
	for i := len(labels) - 1; i >= 0; i-- {
		label := labels[i]

		// Calculate keccak256 of the label
		labelHash := sha3.NewLegacyKeccak256()
		labelHash.Write([]byte(label))
		labelHashBytes := labelHash.Sum(nil)

		// Calculate keccak256 of (current_node + label_hash)
		nodeHash := sha3.NewLegacyKeccak256()
		nodeHash.Write(node)
		nodeHash.Write(labelHashBytes)
		node = nodeHash.Sum(nil)
	}

	return "0x" + hex.EncodeToString(node)
}

// decodeStringResult decodes an ABI-encoded string result
func (c *Client) decodeStringResult(hexData string) (string, error) {
	if len(hexData) < 130 {
		return "", nil
	}

	// Skip function signature and offset (first 64 chars after 0x)
	data := hexData[2:]
	if len(data) < 128 {
		return "", nil
	}

	// Get string length (next 64 chars)
	lengthHex := data[64:128]
	length, err := strconv.ParseInt(lengthHex, 16, 64)
	if err != nil || length <= 0 {
		return "", nil
	}

	// Get string data
	stringData := data[128:]
	if len(stringData) < int(length*2) {
		return "", nil
	}

	// Convert hex to string
	bytes, err := hex.DecodeString(stringData[:length*2])
	if err != nil {
		return "", err
	}

	return string(bytes), nil
}

// resolveENSForward resolves an ENS name to an address (forward lookup)
func (c *Client) resolveENSForward(ctx context.Context, name string) (string, error) {
	// ENS Registry address on mainnet
	ensRegistryAddress := "0x00000000000C2E074eC69A0dFb2997BA6C7d2e1e"

	// This would implement forward ENS resolution
	// For now, return empty to avoid infinite loops
	_ = ensRegistryAddress
	return "", nil
}

// hexToUint64 converts hex string to uint64
func hexToUint64(hex string) (uint64, error) {
	if hex == "" || hex == "0x" {
		return 0, nil
	}
	return strconv.ParseUint(hex[2:], 16, 64)
}
