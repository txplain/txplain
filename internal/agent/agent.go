package agent

import (
	"context"
	"fmt"

	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/chains"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
	"github.com/txplain/txplain/internal/models"
	"github.com/txplain/txplain/internal/rpc"
	txtools "github.com/txplain/txplain/internal/tools"
)

// TxplainAgent orchestrates the transaction explanation workflow
type TxplainAgent struct {
	llm         llms.Model
	rpcClients  map[int64]*rpc.Client
	traceDecoder *txtools.TraceDecoder
	logDecoder   *txtools.LogDecoder
	explainer    *txtools.TransactionExplainer
	executor     *chains.SequentialChain
}

// NewTxplainAgent creates a new transaction explanation agent with RPC-enhanced capabilities
func NewTxplainAgent(openaiAPIKey string, coinMarketCapAPIKey string) (*TxplainAgent, error) {
	// Initialize LLM
	llm, err := openai.New(
		openai.WithModel("gpt-4.1-mini"),
		openai.WithToken(openaiAPIKey),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize LLM: %w", err)
	}

	// Initialize RPC clients for supported networks
	rpcClients := make(map[int64]*rpc.Client)
	for networkID := range models.SupportedNetworks {
		client, err := rpc.NewClient(networkID)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize RPC client for network %d: %w", networkID, err)
		}
		rpcClients[networkID] = client
	}

	// Initialize tools with RPC capabilities - this is the key enhancement!
	// We'll create RPC-enhanced tools when we have the specific network context
	traceDecoder := txtools.NewTraceDecoder() // Will be enhanced per request
	logDecoder := txtools.NewLogDecoder()     // Will be enhanced per request
	
	// Initialize context providers
	var contextProviders []txtools.ContextProvider
	if coinMarketCapAPIKey != "" {
		priceLookup := txtools.NewERC20PriceLookup(coinMarketCapAPIKey)
		contextProviders = append(contextProviders, priceLookup)
	}
	
	explainer := txtools.NewTransactionExplainer(llm, contextProviders...)

	agent := &TxplainAgent{
		llm:          llm,
		rpcClients:   rpcClients,
		traceDecoder: traceDecoder,
		logDecoder:   logDecoder,
		explainer:    explainer,
	}

	return agent, nil
}

// ExplainTransaction processes a transaction with enhanced RPC-based analysis
func (a *TxplainAgent) ExplainTransaction(ctx context.Context, request *models.TransactionRequest) (*models.ExplanationResult, error) {
	// Validate input
	if !models.IsValidNetwork(request.NetworkID) {
		return nil, fmt.Errorf("unsupported network ID: %d", request.NetworkID)
	}

	// Get RPC client for the network
	client, exists := a.rpcClients[request.NetworkID]
	if !exists {
		return nil, fmt.Errorf("no RPC client available for network %d", request.NetworkID)
	}

	// Step 1: Fetch raw transaction data using RPC
	rawData, err := client.FetchTransactionData(ctx, request.TxHash)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch transaction data: %w", err)
	}

	// Step 2: Create RPC-enhanced trace decoder for this specific network
	rpcTraceDecoder := txtools.NewTraceDecoderWithRPC(client)
	
	// Convert RawTransactionData to the format expected by tools
	rawDataMap := map[string]interface{}{
		"tx_hash":    rawData.TxHash,
		"network_id": float64(rawData.NetworkID),
		"trace":      rawData.Trace,
		"logs":       rawData.Logs,
		"receipt":    rawData.Receipt,
		"block":      rawData.Block,
	}
	
	traceInput := map[string]interface{}{
		"raw_data": rawDataMap,
	}

	callsResult, err := rpcTraceDecoder.Run(ctx, traceInput)
	if err != nil {
		return nil, fmt.Errorf("failed to decode traces: %w", err)
	}

	// Step 3: Create RPC-enhanced log decoder  
	rpcLogDecoder := txtools.NewLogDecoderWithRPC(client)
	logInput := map[string]interface{}{
		"raw_data": rawDataMap,
	}

	eventsResult, err := rpcLogDecoder.Run(ctx, logInput)
	if err != nil {
		return nil, fmt.Errorf("failed to decode logs: %w", err)
	}

	// Step 4: Enhance explanation with contract metadata and RPC-derived insights
	explainerInput := map[string]interface{}{
		"calls":    callsResult["calls"],
		"events":   eventsResult["events"],
		"raw_data": rawDataMap,
		"rpc_client": client, // Provide RPC access for additional insights
	}

	explanationResult, err := a.explainer.Run(ctx, explainerInput)
	if err != nil {
		return nil, fmt.Errorf("failed to generate explanation: %w", err)
	}

	// Extract the explanation result
	explanation, ok := explanationResult["explanation"].(*models.ExplanationResult)
	if !ok {
		return nil, fmt.Errorf("invalid explanation result format")
	}

	// Post-process with additional RPC insights
	if err := a.enhanceExplanationWithRPC(ctx, client, explanation); err != nil {
		// Log error but don't fail the whole process
		fmt.Printf("Warning: failed to enhance explanation with RPC data: %v\n", err)
	}

	return explanation, nil
}

// CreateLangChainAgent creates a LangChain agent with registered tools (alternative approach)
func (a *TxplainAgent) CreateLangChainAgent() (*agents.Executor, error) {
	// For now, return a simple implementation - the full LangChain integration 
	// can be enhanced later based on the specific LangChainGo version requirements
	return nil, fmt.Errorf("LangChain agent creation not implemented yet - use ExplainTransaction method instead")
}

// GetSupportedNetworks returns the list of supported networks
func (a *TxplainAgent) GetSupportedNetworks() map[int64]models.Network {
	return models.SupportedNetworks
}

// Close cleans up resources
func (a *TxplainAgent) Close() error {
	// Close RPC clients if needed
	return nil
} 

// enhanceExplanationWithRPC adds additional insights using RPC calls
func (a *TxplainAgent) enhanceExplanationWithRPC(ctx context.Context, client *rpc.Client, explanation *models.ExplanationResult) error {
	// Enhance token transfers with metadata from RPC
	for i, transfer := range explanation.Transfers {
		if transfer.Contract != "" && transfer.Type == "ERC20" {
			// Fetch token metadata via RPC
			if contractInfo, err := client.GetContractInfo(ctx, transfer.Contract); err == nil {
				explanation.Transfers[i].Name = contractInfo.Name
				explanation.Transfers[i].Symbol = contractInfo.Symbol
				explanation.Transfers[i].Decimals = contractInfo.Decimals
			}
		}
	}

	// Enhance wallet effects with token balances (if needed)
	for i, effect := range explanation.Effects {
		// Could fetch current balances for context, but might be expensive
		// For now, just enhance existing transfer data
		for j, transfer := range effect.Transfers {
			if transfer.Contract != "" && transfer.Symbol == "" {
				if contractInfo, err := client.GetContractInfo(ctx, transfer.Contract); err == nil {
					explanation.Effects[i].Transfers[j].Symbol = contractInfo.Symbol
					explanation.Effects[i].Transfers[j].Name = contractInfo.Name
					explanation.Effects[i].Transfers[j].Decimals = contractInfo.Decimals
				}
			}
		}
	}

	// Add metadata about contracts involved
	if explanation.Metadata == nil {
		explanation.Metadata = make(map[string]interface{})
	}

	// Collect all unique contract addresses
	contractAddresses := make(map[string]bool)
	for _, transfer := range explanation.Transfers {
		if transfer.Contract != "" {
			contractAddresses[transfer.Contract] = true
		}
	}

	// Fetch contract info for all involved contracts
	contractInfo := make(map[string]interface{})
	for address := range contractAddresses {
		if info, err := client.GetContractInfo(ctx, address); err == nil && info.Type != "Unknown" {
			contractInfo[address] = map[string]interface{}{
				"type":   info.Type,
				"name":   info.Name,
				"symbol": info.Symbol,
			}
		}
	}

	if len(contractInfo) > 0 {
		explanation.Metadata["contracts"] = contractInfo
	}

	return nil
} 