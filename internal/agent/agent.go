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
	llm                 llms.Model
	rpcClients          map[int64]*rpc.Client
	traceDecoder        *txtools.TraceDecoder
	logDecoder          *txtools.LogDecoder
	explainer           *txtools.TransactionExplainer
	executor            *chains.SequentialChain
	coinMarketCapAPIKey string
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

	// Initialize transaction explainer (now uses baggage pipeline)
	explainer := txtools.NewTransactionExplainer(llm)

	agent := &TxplainAgent{
		llm:                 llm,
		rpcClients:          rpcClients,
		traceDecoder:        traceDecoder,
		logDecoder:          logDecoder,
		explainer:           explainer,
		coinMarketCapAPIKey: coinMarketCapAPIKey,
	}

	return agent, nil
}

// SetVerbose enables or disables verbose logging for the agent
func (a *TxplainAgent) SetVerbose(verbose bool) {
	a.explainer.SetVerbose(verbose)
}

// ExplainTransaction processes a transaction with enhanced baggage pipeline
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

	// Step 2: Initialize baggage with raw transaction data
	baggage := map[string]interface{}{
		"raw_data": map[string]interface{}{
			"tx_hash":    rawData.TxHash,
			"network_id": float64(rawData.NetworkID),
			"trace":      rawData.Trace,
			"logs":       rawData.Logs,
			"receipt":    rawData.Receipt,
			"block":      rawData.Block,
		},
	}

	// Step 3: Create and configure baggage pipeline
	pipeline := txtools.NewBaggagePipeline()
	var contextProviders []txtools.ContextProvider

	// Add static context provider first (loads CSV data - tokens, protocols, addresses)
	staticContextProvider := txtools.NewStaticContextProvider()
	staticContextProvider.SetVerbose(true) // Enable verbose for debugging token data loading
	if err := pipeline.AddProcessor(staticContextProvider); err != nil {
		return nil, fmt.Errorf("failed to add static context provider: %w", err)
	}

	// Add ABI resolver (runs early - fetches contract ABIs from Etherscan)
	abiResolver := txtools.NewABIResolver()
	if err := pipeline.AddProcessor(abiResolver); err != nil {
		return nil, fmt.Errorf("failed to add ABI resolver: %w", err)
	}
	contextProviders = append(contextProviders, abiResolver)

	// Add trace decoder (processes trace data to extract calls with ETH transfers)
	traceDecoder := txtools.NewTraceDecoderWithRPC(client)
	if err := pipeline.AddProcessor(traceDecoder); err != nil {
		return nil, fmt.Errorf("failed to add trace decoder: %w", err)
	}

	// Add log decoder (processes events using resolved ABIs)
	logDecoder := txtools.NewLogDecoderWithRPC(client)
	if err := pipeline.AddProcessor(logDecoder); err != nil {
		return nil, fmt.Errorf("failed to add log decoder: %w", err)
	}

	// Add token transfer extractor (extracts transfers from events)
	transferExtractor := txtools.NewTokenTransferExtractor()
	if err := pipeline.AddProcessor(transferExtractor); err != nil {
		return nil, fmt.Errorf("failed to add token transfer extractor: %w", err)
	}
	contextProviders = append(contextProviders, transferExtractor)

	// Add NFT decoder (extracts and enriches NFT transfers)
	nftDecoder := txtools.NewNFTDecoder()
	nftDecoder.SetRPCClient(client)
	if err := pipeline.AddProcessor(nftDecoder); err != nil {
		return nil, fmt.Errorf("failed to add NFT decoder: %w", err)
	}
	contextProviders = append(contextProviders, nftDecoder)

	// Add token metadata enricher
	tokenMetadata := txtools.NewTokenMetadataEnricher()
	tokenMetadata.SetRPCClient(client)
	if err := pipeline.AddProcessor(tokenMetadata); err != nil {
		return nil, fmt.Errorf("failed to add token metadata enricher: %w", err)
	}
	contextProviders = append(contextProviders, tokenMetadata)

	// Add icon resolver (discovers token icons from TrustWallet GitHub)
	iconResolver := txtools.NewIconResolver(staticContextProvider)
	iconResolver.SetVerbose(true) // Enable verbose for debugging icon discovery
	if err := pipeline.AddProcessor(iconResolver); err != nil {
		return nil, fmt.Errorf("failed to add icon resolver: %w", err)
	}

	// Add price lookup if API key is available
	var priceLookup *txtools.ERC20PriceLookup
	if a.coinMarketCapAPIKey != "" {
		priceLookup = txtools.NewERC20PriceLookup(a.coinMarketCapAPIKey)
		if err := pipeline.AddProcessor(priceLookup); err != nil {
			return nil, fmt.Errorf("failed to add price lookup: %w", err)
		}
		contextProviders = append(contextProviders, priceLookup)

		// Add monetary value enricher (runs after price lookup)
		monetaryEnricher := txtools.NewMonetaryValueEnricher(a.llm, a.coinMarketCapAPIKey)
		if err := pipeline.AddProcessor(monetaryEnricher); err != nil {
			return nil, fmt.Errorf("failed to add monetary value enricher: %w", err)
		}
		contextProviders = append(contextProviders, monetaryEnricher)
	}

	// Add ENS resolver (runs after monetary enrichment)
	ensResolver := txtools.NewENSResolver()
	ensResolver.SetRPCClient(client)
	if err := pipeline.AddProcessor(ensResolver); err != nil {
		return nil, fmt.Errorf("failed to add ENS resolver: %w", err)
	}
	contextProviders = append(contextProviders, ensResolver)

	// Add protocol resolver (probabilistic protocol detection with RAG)
	protocolResolver := txtools.NewProtocolResolver(a.llm)
	protocolResolver.SetConfidenceThreshold(0.6) // 60% minimum confidence
	if err := pipeline.AddProcessor(protocolResolver); err != nil {
		return nil, fmt.Errorf("failed to add protocol resolver: %w", err)
	}
	contextProviders = append(contextProviders, protocolResolver)

	// Add tag resolver (probabilistic tag detection with RAG)
	tagResolver := txtools.NewTagResolver(a.llm)
	tagResolver.SetConfidenceThreshold(0.6) // 60% minimum confidence
	if err := pipeline.AddProcessor(tagResolver); err != nil {
		return nil, fmt.Errorf("failed to add tag resolver: %w", err)
	}
	contextProviders = append(contextProviders, tagResolver)

	// Add context providers to baggage for transaction explainer
	baggage["context_providers"] = contextProviders

	// Add transaction explainer 
	if err := pipeline.AddProcessor(a.explainer); err != nil {
		return nil, fmt.Errorf("failed to add transaction explainer: %w", err)
	}

	// Add annotation generator (runs after explanation is generated)
	annotationGenerator := txtools.NewAnnotationGenerator(a.llm)
	// Enable verbose mode for debugging annotation issues
	annotationGenerator.SetVerbose(true) // Enable for debugging token symbol annotation issues
	// Add all annotation context providers to the generator
	annotationGenerator.AddContextProvider(staticContextProvider)
	annotationGenerator.AddContextProvider(tokenMetadata)
	annotationGenerator.AddContextProvider(iconResolver)
	annotationGenerator.AddContextProvider(protocolResolver)
	annotationGenerator.AddContextProvider(tagResolver)
	annotationGenerator.AddContextProvider(ensResolver) // CRITICAL: Add ENS resolver for address mapping
	if priceLookup != nil {
		annotationGenerator.AddContextProvider(priceLookup)
	}
	
	// Add monetary enricher if available (for gas fee tooltips)
	for _, provider := range contextProviders {
		if monetaryProvider, ok := provider.(*txtools.MonetaryValueEnricher); ok {
			annotationGenerator.AddContextProvider(monetaryProvider)
			break
		}
	}
	if err := pipeline.AddProcessor(annotationGenerator); err != nil {
		return nil, fmt.Errorf("failed to add annotation generator: %w", err)
	}

	// Step 4: Execute the pipeline
	if err := pipeline.Execute(ctx, baggage); err != nil {
		return nil, fmt.Errorf("pipeline execution failed: %w", err)
	}

	// Step 5: Extract the explanation result
	explanation, ok := baggage["explanation"].(*models.ExplanationResult)
	if !ok {
		return nil, fmt.Errorf("invalid explanation result format")
	}

	// Store cleaned baggage data in explanation metadata for debugging (exclude circular references)
	if explanation.Metadata == nil {
		explanation.Metadata = make(map[string]interface{})
	}
	
	// Create a clean copy of baggage without circular references
	cleanBaggage := make(map[string]interface{})
	for key, value := range baggage {
		// Skip fields that might contain circular references
		if key == "explanation" || key == "context_providers" {
			cleanBaggage[key] = fmt.Sprintf("<%s - excluded to prevent circular reference>", key)
		} else {
			cleanBaggage[key] = value
		}
	}
	explanation.Metadata["pipeline_baggage"] = cleanBaggage

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
