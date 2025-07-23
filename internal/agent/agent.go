package agent

import (
	"context"
	"fmt"
	"strings"

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

	// Initialize static context provider first (needed by transaction explainer)
	staticProvider := txtools.NewStaticContextProvider()

	// Initialize transaction explainer (now uses baggage pipeline with RAG)
	explainer := txtools.NewTransactionExplainer(llm, staticProvider)

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
	fmt.Println("\n" + strings.Repeat("🌟", 40))
	fmt.Printf("🔍 ANALYZING TRANSACTION: %s\n", request.TxHash)
	fmt.Printf("🌐 Network: %s (ID: %d)\n", func() string {
		if network, exists := models.GetNetwork(request.NetworkID); exists {
			return network.Name
		}
		return "Unknown"
	}(), request.NetworkID)
	fmt.Println(strings.Repeat("🌟", 40))

	// Validate input
	if !models.IsValidNetwork(request.NetworkID) {
		return nil, fmt.Errorf("unsupported network ID: %d", request.NetworkID)
	}

	// Get RPC client for the network
	client, exists := a.rpcClients[request.NetworkID]
	if !exists {
		return nil, fmt.Errorf("no RPC client available for network %d", request.NetworkID)
	}

	fmt.Println("\n📡 STEP 1: Fetching raw transaction data...")
	// Step 1: Fetch raw transaction data using RPC
	rawData, err := client.FetchTransactionData(ctx, request.TxHash)
	if err != nil {
		fmt.Printf("❌ Failed to fetch transaction data: %v\n", err)
		return nil, fmt.Errorf("failed to fetch transaction data: %w", err)
	}
	fmt.Printf("✅ Raw data fetched: %d logs, trace available: %t\n", len(rawData.Logs), rawData.Trace != nil)

	fmt.Println("\n🎒 STEP 2: Initializing baggage...")
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
	fmt.Printf("✅ Baggage initialized with %d items\n", len(baggage))

	fmt.Println("\n🔧 STEP 3: Configuring processing pipeline...")
	// Step 3: Create and configure baggage pipeline
	pipeline := txtools.NewBaggagePipeline()
	pipeline.SetVerbose(true) // Enable verbose logging for all pipeline steps
	var contextProviders []interface{}

	fmt.Println("   📥 Adding pipeline processors...")

	// Add static context provider first (loads CSV data - tokens, protocols, addresses)
	fmt.Println("      • Static Context Provider (CSV data loader)")
	staticContextProvider := txtools.NewStaticContextProvider()
	staticContextProvider.SetVerbose(true) // Enable verbose for debugging token data loading
	if err := pipeline.AddProcessor(staticContextProvider); err != nil {
		return nil, fmt.Errorf("failed to add static context provider: %w", err)
	}

	// Add transaction context provider (processes raw transaction data)
	fmt.Println("      • Transaction Context Provider")
	transactionContextProvider := txtools.NewTransactionContextProvider()
	if err := pipeline.AddProcessor(transactionContextProvider); err != nil {
		return nil, fmt.Errorf("failed to add transaction context provider: %w", err)
	}
	contextProviders = append(contextProviders, transactionContextProvider)

	// Add ABI resolver (runs early - fetches contract ABIs from Etherscan)
	fmt.Println("      • ABI Resolver (Etherscan)")
	abiResolver := txtools.NewABIResolver()
	if err := pipeline.AddProcessor(abiResolver); err != nil {
		return nil, fmt.Errorf("failed to add ABI resolver: %w", err)
	}
	contextProviders = append(contextProviders, abiResolver)

	// Add trace decoder (processes trace data to extract calls with ETH transfers)
	fmt.Println("      • Trace Decoder (function calls & ETH transfers)")
	traceDecoder := txtools.NewTraceDecoderWithRPC(client)
	if err := pipeline.AddProcessor(traceDecoder); err != nil {
		return nil, fmt.Errorf("failed to add trace decoder: %w", err)
	}
	contextProviders = append(contextProviders, traceDecoder)

	// Add log decoder (processes events using resolved ABIs)
	fmt.Println("      • Log Decoder (event decoding)")
	logDecoder := txtools.NewLogDecoderWithRPC(client)
	if err := pipeline.AddProcessor(logDecoder); err != nil {
		return nil, fmt.Errorf("failed to add log decoder: %w", err)
	}
	contextProviders = append(contextProviders, logDecoder)

	// Add signature resolver (4byte.directory lookup for missing signatures)
	fmt.Println("      • Signature Resolver (4byte.directory)")
	signatureResolver := txtools.NewSignatureResolver()
	if err := pipeline.AddProcessor(signatureResolver); err != nil {
		return nil, fmt.Errorf("failed to add signature resolver: %w", err)
	}
	contextProviders = append(contextProviders, signatureResolver)

	// Add token transfer extractor (extracts transfers from events)
	fmt.Println("      • Token Transfer Extractor")
	transferExtractor := txtools.NewTokenTransferExtractor()
	if err := pipeline.AddProcessor(transferExtractor); err != nil {
		return nil, fmt.Errorf("failed to add token transfer extractor: %w", err)
	}
	contextProviders = append(contextProviders, transferExtractor)

	// Add NFT decoder (extracts and enriches NFT transfers)
	fmt.Println("      • NFT Decoder")
	nftDecoder := txtools.NewNFTDecoder()
	nftDecoder.SetRPCClient(client)
	if err := pipeline.AddProcessor(nftDecoder); err != nil {
		return nil, fmt.Errorf("failed to add NFT decoder: %w", err)
	}
	contextProviders = append(contextProviders, nftDecoder)

	// Add token metadata enricher
	fmt.Println("      • Token Metadata Enricher")
	tokenMetadata := txtools.NewTokenMetadataEnricher()
	tokenMetadata.SetRPCClient(client)
	if err := pipeline.AddProcessor(tokenMetadata); err != nil {
		return nil, fmt.Errorf("failed to add token metadata enricher: %w", err)
	}
	contextProviders = append(contextProviders, tokenMetadata)

	// Add amounts finder (NEW - uses LLM to detect ALL relevant amounts generically)
	fmt.Println("      • Amounts Finder (AI-powered)")
	amountsFinder := txtools.NewAmountsFinder(a.llm)
	if err := pipeline.AddProcessor(amountsFinder); err != nil {
		return nil, fmt.Errorf("failed to add amounts finder: %w", err)
	}
	contextProviders = append(contextProviders, amountsFinder)

	// Add icon resolver (discovers token icons from TrustWallet GitHub)
	fmt.Println("      • Icon Resolver (TrustWallet)")
	iconResolver := txtools.NewIconResolver(staticContextProvider)
	iconResolver.SetVerbose(true) // Enable verbose for debugging icon discovery
	if err := pipeline.AddProcessor(iconResolver); err != nil {
		return nil, fmt.Errorf("failed to add icon resolver: %w", err)
	}

	// Add price lookup if API key is available (runs AFTER amounts_finder)
	var priceLookup *txtools.ERC20PriceLookup
	if a.coinMarketCapAPIKey != "" {
		fmt.Println("      • ERC20 Price Lookup (CoinMarketCap)")
		priceLookup = txtools.NewERC20PriceLookup(a.coinMarketCapAPIKey)
		if err := pipeline.AddProcessor(priceLookup); err != nil {
			return nil, fmt.Errorf("failed to add price lookup: %w", err)
		}
		contextProviders = append(contextProviders, priceLookup)

		// Add monetary value enricher (runs after amounts_finder + price lookup)
		fmt.Println("      • Monetary Value Enricher (AI-powered)")
		monetaryEnricher := txtools.NewMonetaryValueEnricher(a.llm, a.coinMarketCapAPIKey)
		if err := pipeline.AddProcessor(monetaryEnricher); err != nil {
			return nil, fmt.Errorf("failed to add monetary value enricher: %w", err)
		}
		contextProviders = append(contextProviders, monetaryEnricher)
	} else {
		fmt.Println("      • ERC20 Price Lookup: SKIPPED (no CoinMarketCap API key)")
	}

	// Add ENS resolver (runs after monetary enrichment)
	fmt.Println("      • ENS Resolver")
	ensResolver := txtools.NewENSResolver()
	ensResolver.SetRPCClient(client)
	if err := pipeline.AddProcessor(ensResolver); err != nil {
		return nil, fmt.Errorf("failed to add ENS resolver: %w", err)
	}
	contextProviders = append(contextProviders, ensResolver)

	// Add address role resolver (runs after basic data gathering, before analysis tools)
	fmt.Println("      • Address Role Resolver (AI-powered)")
	addressRoleResolver := txtools.NewAddressRoleResolver(a.llm)
	addressRoleResolver.SetRPCClient(client)
	addressRoleResolver.SetVerbose(true) // Enable verbose for debugging
	if err := pipeline.AddProcessor(addressRoleResolver); err != nil {
		return nil, fmt.Errorf("failed to add address role resolver: %w", err)
	}
	contextProviders = append(contextProviders, addressRoleResolver)

	// Add protocol resolver (probabilistic protocol detection with RAG)
	fmt.Println("      • Protocol Resolver (AI-powered)")
	protocolResolver := txtools.NewProtocolResolver(a.llm)
	protocolResolver.SetConfidenceThreshold(0.6) // 60% minimum confidence
	if err := pipeline.AddProcessor(protocolResolver); err != nil {
		return nil, fmt.Errorf("failed to add protocol resolver: %w", err)
	}
	contextProviders = append(contextProviders, protocolResolver)

	// Add tag resolver (probabilistic tag detection with RAG)
	fmt.Println("      • Tag Resolver (AI-powered)")
	tagResolver := txtools.NewTagResolver(a.llm)
	tagResolver.SetConfidenceThreshold(0.6) // 60% minimum confidence
	if err := pipeline.AddProcessor(tagResolver); err != nil {
		return nil, fmt.Errorf("failed to add tag resolver: %w", err)
	}
	contextProviders = append(contextProviders, tagResolver)

	// Add context providers to baggage for transaction explainer
	baggage["context_providers"] = contextProviders

	// Add transaction explainer
	fmt.Println("      • Transaction Explainer (AI-powered with RAG)")
	if err := pipeline.AddProcessor(a.explainer); err != nil {
		return nil, fmt.Errorf("failed to add transaction explainer: %w", err)
	}

	// Add annotation generator (runs after explanation is generated)
	// Now uses GetPromptContext from all context providers - much simpler!
	fmt.Println("      • Annotation Generator (AI-powered)")
	annotationGenerator := txtools.NewAnnotationGenerator(a.llm)
	annotationGenerator.SetVerbose(true) // Enable for debugging
	if err := pipeline.AddProcessor(annotationGenerator); err != nil {
		return nil, fmt.Errorf("failed to add annotation generator: %w", err)
	}

	fmt.Printf("✅ Pipeline configured with %d processors\n", pipeline.GetProcessorCount())

	// Step 4: Execute the pipeline
	if err := pipeline.Execute(ctx, baggage); err != nil {
		fmt.Printf("❌ Pipeline execution failed: %v\n", err)
		return nil, fmt.Errorf("pipeline execution failed: %w", err)
	}

	fmt.Println("📦 STEP 4: Extracting final results...")
	// Step 5: Extract the explanation result
	explanation, ok := baggage["explanation"].(*models.ExplanationResult)
	if !ok {
		fmt.Println("❌ Invalid explanation result format")
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

	fmt.Println("\n" + strings.Repeat("🎉", 40))
	fmt.Printf("✅ TRANSACTION ANALYSIS COMPLETE!\n")
	fmt.Printf("📝 Summary: %s\n", explanation.Summary)
	if len(explanation.Tags) > 0 {
		fmt.Printf("🏷️  Tags: %v\n", explanation.Tags)
	}
	fmt.Printf("💰 Gas Used: %d\n", explanation.GasUsed)
	fmt.Printf("🔗 Explorer Links: %d\n", len(explanation.Links))
	fmt.Println(strings.Repeat("🎉", 40) + "\n")

	return explanation, nil
}

// ExplainTransactionWithProgress processes a transaction with real-time progress updates
func (a *TxplainAgent) ExplainTransactionWithProgress(ctx context.Context, request *models.TransactionRequest, progressChan chan<- models.ProgressEvent) (*models.ExplanationResult, error) {
	// Create progress tracker
	progressTracker := models.NewProgressTracker(progressChan)
	defer progressTracker.Close() // Ensure heartbeat goroutine is stopped

	// Send immediate progress update for instant feedback
	progressTracker.UpdateComponent("analysis_start", models.ComponentGroupData, "Starting Analysis", models.ComponentStatusRunning, fmt.Sprintf("Initializing analysis for transaction %s...", request.TxHash[:10]+"..."))

	fmt.Println("\n" + strings.Repeat("🌟", 40))
	fmt.Printf("🔍 ANALYZING TRANSACTION: %s\n", request.TxHash)
	fmt.Printf("🌐 Network: %s (ID: %d)\n", func() string {
		if network, exists := models.GetNetwork(request.NetworkID); exists {
			return network.Name
		}
		return "Unknown"
	}(), request.NetworkID)
	fmt.Println(strings.Repeat("🌟", 40))

	// Validate input
	if !models.IsValidNetwork(request.NetworkID) {
		err := fmt.Errorf("unsupported network ID: %d", request.NetworkID)
		progressTracker.SendError(err)
		return nil, err
	}

	// Get RPC client for the network
	client, exists := a.rpcClients[request.NetworkID]
	if !exists {
		err := fmt.Errorf("no RPC client available for network %d", request.NetworkID)
		progressTracker.SendError(err)
		return nil, err
	}

	// Complete the initialization step and start fetching data
	progressTracker.UpdateComponent("analysis_start", models.ComponentGroupData, "Starting Analysis", models.ComponentStatusFinished, "Analysis initialized successfully")
	
	// Update progress - fetching transaction data (this is now the first real step)
	progressTracker.UpdateComponent("fetch_data", models.ComponentGroupData, "Fetching Transaction Data", models.ComponentStatusRunning, "Getting transaction details from blockchain...")

	// Step 1: Fetch raw transaction data using RPC
	rawData, err := client.FetchTransactionData(ctx, request.TxHash)
	if err != nil {
		fmt.Printf("❌ Failed to fetch transaction data: %v\n", err)
		progressTracker.SendError(fmt.Errorf("failed to fetch transaction data: %w", err))
		return nil, fmt.Errorf("failed to fetch transaction data: %w", err)
	}

	// Mark data fetching as complete
	progressTracker.UpdateComponent("fetch_data", models.ComponentGroupData, "Fetching Transaction Data", models.ComponentStatusFinished, fmt.Sprintf("Fetched %d logs and trace data", len(rawData.Logs)))

	fmt.Printf("✅ Raw data fetched: %d logs, trace available: %t\n", len(rawData.Logs), rawData.Trace != nil)

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

	// Step 3: Create and configure baggage pipeline WITH PROGRESS
	pipeline := txtools.NewBaggagePipelineWithProgress(progressChan)
	pipeline.SetVerbose(true)
	var contextProviders []interface{}

	// Add static context provider first (loads CSV data - tokens, protocols, addresses)
	staticContextProvider := txtools.NewStaticContextProvider()
	staticContextProvider.SetVerbose(true)
	if err := pipeline.AddProcessor(staticContextProvider); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add static context provider: %w", err))
		return nil, fmt.Errorf("failed to add static context provider: %w", err)
	}

	// Add transaction context provider (processes raw transaction data)
	transactionContextProvider := txtools.NewTransactionContextProvider()
	if err := pipeline.AddProcessor(transactionContextProvider); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add transaction context provider: %w", err))
		return nil, fmt.Errorf("failed to add transaction context provider: %w", err)
	}
	contextProviders = append(contextProviders, transactionContextProvider)

	// Add ABI resolver (runs early - fetches contract ABIs from Etherscan)
	abiResolver := txtools.NewABIResolver()
	if err := pipeline.AddProcessor(abiResolver); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add ABI resolver: %w", err))
		return nil, fmt.Errorf("failed to add ABI resolver: %w", err)
	}
	contextProviders = append(contextProviders, abiResolver)

	// Add trace decoder (processes trace data to extract calls with ETH transfers)
	traceDecoder := txtools.NewTraceDecoderWithRPC(client)
	if err := pipeline.AddProcessor(traceDecoder); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add trace decoder: %w", err))
		return nil, fmt.Errorf("failed to add trace decoder: %w", err)
	}
	contextProviders = append(contextProviders, traceDecoder)

	// Add log decoder (processes events using resolved ABIs)
	logDecoder := txtools.NewLogDecoderWithRPC(client)
	if err := pipeline.AddProcessor(logDecoder); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add log decoder: %w", err))
		return nil, fmt.Errorf("failed to add log decoder: %w", err)
	}
	contextProviders = append(contextProviders, logDecoder)

	// Add signature resolver (4byte.directory lookup for missing signatures)
	signatureResolver := txtools.NewSignatureResolver()
	if err := pipeline.AddProcessor(signatureResolver); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add signature resolver: %w", err))
		return nil, fmt.Errorf("failed to add signature resolver: %w", err)
	}
	contextProviders = append(contextProviders, signatureResolver)

	// Add token transfer extractor (extracts transfers from events)
	transferExtractor := txtools.NewTokenTransferExtractor()
	if err := pipeline.AddProcessor(transferExtractor); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add token transfer extractor: %w", err))
		return nil, fmt.Errorf("failed to add token transfer extractor: %w", err)
	}
	contextProviders = append(contextProviders, transferExtractor)

	// Add NFT decoder (extracts and enriches NFT transfers)
	nftDecoder := txtools.NewNFTDecoder()
	nftDecoder.SetRPCClient(client)
	if err := pipeline.AddProcessor(nftDecoder); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add NFT decoder: %w", err))
		return nil, fmt.Errorf("failed to add NFT decoder: %w", err)
	}
	contextProviders = append(contextProviders, nftDecoder)

	// Add token metadata enricher
	tokenMetadata := txtools.NewTokenMetadataEnricher()
	tokenMetadata.SetRPCClient(client)
	if err := pipeline.AddProcessor(tokenMetadata); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add token metadata enricher: %w", err))
		return nil, fmt.Errorf("failed to add token metadata enricher: %w", err)
	}
	contextProviders = append(contextProviders, tokenMetadata)

	// Add amounts finder (NEW - uses LLM to detect ALL relevant amounts generically)
	amountsFinder := txtools.NewAmountsFinder(a.llm)
	if err := pipeline.AddProcessor(amountsFinder); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add amounts finder: %w", err))
		return nil, fmt.Errorf("failed to add amounts finder: %w", err)
	}
	contextProviders = append(contextProviders, amountsFinder)

	// Add icon resolver (discovers token icons from TrustWallet GitHub)
	iconResolver := txtools.NewIconResolver(staticContextProvider)
	iconResolver.SetVerbose(true)
	if err := pipeline.AddProcessor(iconResolver); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add icon resolver: %w", err))
		return nil, fmt.Errorf("failed to add icon resolver: %w", err)
	}

	// Add price lookup if API key is available (runs AFTER amounts_finder)
	if a.coinMarketCapAPIKey != "" {
		priceLookup := txtools.NewERC20PriceLookup(a.coinMarketCapAPIKey)
		if err := pipeline.AddProcessor(priceLookup); err != nil {
			progressTracker.SendError(fmt.Errorf("failed to add price lookup: %w", err))
			return nil, fmt.Errorf("failed to add price lookup: %w", err)
		}
		contextProviders = append(contextProviders, priceLookup)

		// Add monetary value enricher (runs after amounts_finder + price lookup)
		monetaryEnricher := txtools.NewMonetaryValueEnricher(a.llm, a.coinMarketCapAPIKey)
		if err := pipeline.AddProcessor(monetaryEnricher); err != nil {
			progressTracker.SendError(fmt.Errorf("failed to add monetary value enricher: %w", err))
			return nil, fmt.Errorf("failed to add monetary value enricher: %w", err)
		}
		contextProviders = append(contextProviders, monetaryEnricher)
	}

	// Add ENS resolver (runs after monetary enrichment)
	ensResolver := txtools.NewENSResolver()
	ensResolver.SetRPCClient(client)
	if err := pipeline.AddProcessor(ensResolver); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add ENS resolver: %w", err))
		return nil, fmt.Errorf("failed to add ENS resolver: %w", err)
	}
	contextProviders = append(contextProviders, ensResolver)

	// Add address role resolver (runs after basic data gathering, before analysis tools)
	addressRoleResolver := txtools.NewAddressRoleResolver(a.llm)
	addressRoleResolver.SetRPCClient(client)
	addressRoleResolver.SetVerbose(true)
	if err := pipeline.AddProcessor(addressRoleResolver); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add address role resolver: %w", err))
		return nil, fmt.Errorf("failed to add address role resolver: %w", err)
	}
	contextProviders = append(contextProviders, addressRoleResolver)

	// Add protocol resolver (probabilistic protocol detection with RAG)
	protocolResolver := txtools.NewProtocolResolver(a.llm)
	protocolResolver.SetConfidenceThreshold(0.6)
	if err := pipeline.AddProcessor(protocolResolver); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add protocol resolver: %w", err))
		return nil, fmt.Errorf("failed to add protocol resolver: %w", err)
	}
	contextProviders = append(contextProviders, protocolResolver)

	// Add tag resolver (probabilistic tag detection with RAG)
	tagResolver := txtools.NewTagResolver(a.llm)
	tagResolver.SetConfidenceThreshold(0.6)
	if err := pipeline.AddProcessor(tagResolver); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add tag resolver: %w", err))
		return nil, fmt.Errorf("failed to add tag resolver: %w", err)
	}
	contextProviders = append(contextProviders, tagResolver)

	// Add context providers to baggage for transaction explainer
	baggage["context_providers"] = contextProviders

	// Add transaction explainer
	if err := pipeline.AddProcessor(a.explainer); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add transaction explainer: %w", err))
		return nil, fmt.Errorf("failed to add transaction explainer: %w", err)
	}

	// Add annotation generator (runs after explanation is generated)
	annotationGenerator := txtools.NewAnnotationGenerator(a.llm)
	annotationGenerator.SetVerbose(true)
	if err := pipeline.AddProcessor(annotationGenerator); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add annotation generator: %w", err))
		return nil, fmt.Errorf("failed to add annotation generator: %w", err)
	}

	// Execute the pipeline with progress tracking
	if err := pipeline.Execute(ctx, baggage); err != nil {
		fmt.Printf("❌ Pipeline execution failed: %v\n", err)
		progressTracker.SendError(fmt.Errorf("pipeline execution failed: %w", err))
		return nil, fmt.Errorf("pipeline execution failed: %w", err)
	}

	// Extract the explanation result
	explanation, ok := baggage["explanation"].(*models.ExplanationResult)
	if !ok {
		err := fmt.Errorf("invalid explanation result format")
		progressTracker.SendError(err)
		return nil, err
	}

	// Store cleaned baggage data in explanation metadata for debugging
	if explanation.Metadata == nil {
		explanation.Metadata = make(map[string]interface{})
	}
	cleanBaggage := make(map[string]interface{})
	for key, value := range baggage {
		if key == "explanation" || key == "context_providers" || key == "progress_tracker" {
			cleanBaggage[key] = fmt.Sprintf("<%s - excluded to prevent circular reference>", key)
		} else {
			cleanBaggage[key] = value
		}
	}
	explanation.Metadata["pipeline_baggage"] = cleanBaggage

	// Send completion event
	progressTracker.SendComplete(explanation)

	fmt.Println("\n" + strings.Repeat("🎉", 40))
	fmt.Printf("✅ TRANSACTION ANALYSIS COMPLETE!\n")
	fmt.Printf("📝 Summary: %s\n", explanation.Summary)
	if len(explanation.Tags) > 0 {
		fmt.Printf("🏷️  Tags: %v\n", explanation.Tags)
	}
	fmt.Printf("💰 Gas Used: %d\n", explanation.GasUsed)
	fmt.Printf("🔗 Explorer Links: %d\n", len(explanation.Links))
	fmt.Println(strings.Repeat("🎉", 40) + "\n")

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
