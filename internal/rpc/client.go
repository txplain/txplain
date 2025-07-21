package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/txplain/txplain/internal/models"
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
			Timeout: 30 * time.Second,
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

	var rpcResp JSONRPCResponse
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
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

	switch c.network.ID {
	case 1: // Ethereum
		method = "debug_traceTransaction"
		params = []interface{}{txHash, map[string]interface{}{
			"tracer": "callTracer",
		}}
	case 137: // Polygon
		method = "debug_traceTransaction"
		params = []interface{}{txHash, map[string]interface{}{
			"tracer": "callTracer",
		}}
	case 42161: // Arbitrum
		method = "arbtrace_transaction"
		params = []string{txHash}
	default:
		return nil, fmt.Errorf("trace not supported for network %d", c.network.ID)
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

// hexToUint64 converts hex string to uint64
func hexToUint64(hex string) (uint64, error) {
	if hex == "" || hex == "0x" {
		return 0, nil
	}
	return strconv.ParseUint(hex[2:], 16, 64)
} 