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
	cmcClient           *txtools.CoinMarketCapClient // Centralized CMC client
	cache               txtools.Cache
	verbose             bool
}

// NewTxplainAgent creates a new transaction explanation agent with RPC-enhanced capabilities
func NewTxplainAgent(openaiAPIKey string, coinMarketCapAPIKey string, cache txtools.Cache, verbose bool) (*TxplainAgent, error) {
	// Initialize LLM
	llm, err := openai.New(
		openai.WithModel("gpt-4.1-mini"),
		openai.WithToken(openaiAPIKey),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize LLM: %w", err)
	}

	// Initialize centralized CoinMarketCap client
	cmcClient := txtools.NewCoinMarketCapClient(coinMarketCapAPIKey, cache, verbose)
	if verbose {
		fmt.Printf("🪙 CoinMarketCap client initialized (API available: %t)\n", cmcClient.IsAvailable())
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
	traceDecoder := txtools.NewTraceDecoder(cache, verbose) // Will be enhanced per request
	logDecoder := txtools.NewLogDecoder(cache, verbose)     // Will be enhanced per request

	// Initialize static context provider first (needed by transaction explainer)
	staticProvider := txtools.NewStaticContextProvider(verbose)

	// Initialize transaction explainer (now uses baggage pipeline with RAG)
	explainer := txtools.NewTransactionExplainer(llm, staticProvider, verbose)

	agent := &TxplainAgent{
		llm:                 llm,
		rpcClients:          rpcClients,
		traceDecoder:        traceDecoder,
		logDecoder:          logDecoder,
		explainer:           explainer,
		coinMarketCapAPIKey: coinMarketCapAPIKey,
		cmcClient:           cmcClient, // Store centralized client
		cache:               cache,
		verbose:             verbose,
	}

	return agent, nil
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
	pipeline := txtools.NewBaggagePipeline(true) // Enable verbose logging for all pipeline steps
	var contextProviders []interface{}

	fmt.Println("   📥 Adding pipeline processors...")

	// Add static context provider first (loads CSV data - tokens, protocols, addresses)
	fmt.Println("      • Static Context Provider (CSV data loader)")
	staticContextProvider := txtools.NewStaticContextProvider(true) // Enable verbose for debugging token data loading
	if err := pipeline.AddProcessor(staticContextProvider); err != nil {
		return nil, fmt.Errorf("failed to add static context provider: %w", err)
	}

	// Add transaction context provider (processes raw transaction data)
	fmt.Println("      • Transaction Context Provider")
	transactionContextProvider := txtools.NewTransactionContextProvider(true)
	if err := pipeline.AddProcessor(transactionContextProvider); err != nil {
		return nil, fmt.Errorf("failed to add transaction context provider: %w", err)
	}
	contextProviders = append(contextProviders, transactionContextProvider)

	// Add ABI resolver (runs early - fetches contract ABIs from Etherscan)
	fmt.Println("      • ABI Resolver (Etherscan)")
	abiResolver := txtools.NewABIResolver(a.cache, a.verbose)
	if err := pipeline.AddProcessor(abiResolver); err != nil {
		return nil, fmt.Errorf("failed to add ABI resolver: %w", err)
	}
	contextProviders = append(contextProviders, abiResolver)

	// Add trace decoder (processes trace data to extract calls with ETH transfers)
	fmt.Println("      • Trace Decoder (function calls & ETH transfers)")
	traceDecoder := txtools.NewTraceDecoderWithRPC(client, a.cache, a.verbose)
	if err := pipeline.AddProcessor(traceDecoder); err != nil {
		return nil, fmt.Errorf("failed to add trace decoder: %w", err)
	}
	contextProviders = append(contextProviders, traceDecoder)

	// Add log decoder (processes events using resolved ABIs)
	fmt.Println("      • Log Decoder (event decoding)")
	logDecoder := txtools.NewLogDecoderWithRPC(client, a.cache)
	if err := pipeline.AddProcessor(logDecoder); err != nil {
		return nil, fmt.Errorf("failed to add log decoder: %w", err)
	}
	contextProviders = append(contextProviders, logDecoder)

	// Add signature resolver (4byte.directory lookup for missing signatures)
	fmt.Println("      • Signature Resolver (4byte.directory)")
	signatureResolver := txtools.NewSignatureResolver(a.cache, a.verbose)
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
	nftDecoder := txtools.NewNFTDecoder(a.cache, a.verbose, client)
	if err := pipeline.AddProcessor(nftDecoder); err != nil {
		return nil, fmt.Errorf("failed to add NFT decoder: %w", err)
	}
	contextProviders = append(contextProviders, nftDecoder)

	// Add token metadata enricher
	fmt.Println("      • Token Metadata Enricher")
	tokenMetadata := txtools.NewTokenMetadataEnricher(a.cache, a.verbose, client, a.cmcClient)
	if err := pipeline.AddProcessor(tokenMetadata); err != nil {
		return nil, fmt.Errorf("failed to add token metadata enricher: %w", err)
	}
	contextProviders = append(contextProviders, tokenMetadata)

	// Add amounts finder (NEW - uses LLM to detect ALL relevant amounts generically)
	fmt.Println("      • Amounts Finder (AI-powered)")
	amountsFinder := txtools.NewAmountsFinder(a.llm, a.verbose)
	if err := pipeline.AddProcessor(amountsFinder); err != nil {
		return nil, fmt.Errorf("failed to add amounts finder: %w", err)
	}
	contextProviders = append(contextProviders, amountsFinder)

	// Add icon resolver (discovers token icons from CoinMarketCap + TrustWallet)
	fmt.Println("      • Icon Resolver (CoinMarketCap + TrustWallet)")
	iconResolver := txtools.NewIconResolverWithCMC(staticContextProvider, a.cache, a.verbose, a.cmcClient)
	if err := pipeline.AddProcessor(iconResolver); err != nil {
		return nil, fmt.Errorf("failed to add icon resolver: %w", err)
	}

	// Add price lookup (now uses centralized CMC client)
	if a.cmcClient.IsAvailable() {
		fmt.Println("      • ERC20 Price Lookup (CoinMarketCap)")
		priceLookup := txtools.NewERC20PriceLookup(a.cmcClient, a.cache, a.verbose)
		if err := pipeline.AddProcessor(priceLookup); err != nil {
			return nil, fmt.Errorf("failed to add price lookup: %w", err)
		}
		contextProviders = append(contextProviders, priceLookup)

		// Add monetary value enricher (now uses centralized CMC client)
		fmt.Println("      • Monetary Value Enricher (AI-powered)")
		monetaryEnricher := txtools.NewMonetaryValueEnricher(a.llm, a.cmcClient, a.cache, a.verbose)
		if err := pipeline.AddProcessor(monetaryEnricher); err != nil {
			return nil, fmt.Errorf("failed to add monetary value enricher: %w", err)
		}
		contextProviders = append(contextProviders, monetaryEnricher)
	} else {
		fmt.Println("      • ERC20 Price Lookup: SKIPPED (no CoinMarketCap API key)")
	}

	// Add ENS resolver (runs after monetary enrichment)
	fmt.Println("      • ENS Resolver")
	ensResolver := txtools.NewENSResolver(a.cache, a.verbose, client)
	if err := pipeline.AddProcessor(ensResolver); err != nil {
		return nil, fmt.Errorf("failed to add ENS resolver: %w", err)
	}
	contextProviders = append(contextProviders, ensResolver)

	// Add address role resolver (runs after basic data gathering, before analysis tools)
	fmt.Println("      • Address Role Resolver (AI-powered)")
	addressRoleResolver := txtools.NewAddressRoleResolver(a.llm, a.verbose, client)
	if err := pipeline.AddProcessor(addressRoleResolver); err != nil {
		return nil, fmt.Errorf("failed to add address role resolver: %w", err)
	}
	contextProviders = append(contextProviders, addressRoleResolver)

	// Add protocol resolver (probabilistic protocol detection with RAG)
	fmt.Println("      • Protocol Resolver (AI-powered)")
	protocolResolver := txtools.NewProtocolResolver(a.llm, a.verbose, 0.6)
	if err := pipeline.AddProcessor(protocolResolver); err != nil {
		return nil, fmt.Errorf("failed to add protocol resolver: %w", err)
	}
	contextProviders = append(contextProviders, protocolResolver)

	// Add tag resolver (probabilistic tag detection with RAG)
	fmt.Println("      • Tag Resolver (AI-powered)")
	tagResolver := txtools.NewTagResolver(a.llm, a.verbose, 0.6)
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
	annotationGenerator := txtools.NewAnnotationGenerator(a.llm, a.verbose)
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

	// Create a clean copy of baggage without circular references or sensitive data
	cleanBaggage := make(map[string]interface{})
	for key, value := range baggage {
		// Skip fields that might contain circular references or sensitive data
		if key == "explanation" || key == "context_providers" {
			cleanBaggage[key] = fmt.Sprintf("<%s - excluded to prevent circular reference>", key)
		} else {
			// For security, exclude raw RPC responses and potentially sensitive debug data
			// Only include safe, sanitized metadata for production use
			switch key {
			case "raw_data":
				// Don't include raw blockchain data in metadata - it's large and unnecessary
				cleanBaggage[key] = "<raw_data - excluded for security and size>"
			case "rpc_responses", "api_responses", "debug_info":
				// Exclude any RPC or API responses that might contain endpoint info
				cleanBaggage[key] = fmt.Sprintf("<%s - excluded for security>", key)
			default:
				// Include other metadata but ensure no sensitive data
				cleanBaggage[key] = value
			}
		}
	}
	// Only include baggage metadata in non-production environments for debugging
	// In production, this could potentially leak sensitive information
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
	pipeline := txtools.NewBaggagePipelineWithProgress(progressChan, a.verbose)
	var contextProviders []interface{}

	// Send progress update for pipeline setup
	progressTracker.UpdateComponent("pipeline_setup", models.ComponentGroupData, "Configuring Pipeline", models.ComponentStatusRunning, "Adding data processing tools...")

	// Add static context provider first (loads CSV data - tokens, protocols, addresses)
	staticContextProvider := txtools.NewStaticContextProvider(a.verbose)
	if err := pipeline.AddProcessor(staticContextProvider); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add static context provider: %w", err))
		return nil, fmt.Errorf("failed to add static context provider: %w", err)
	}

	// Add transaction context provider (processes raw transaction data)
	progressTracker.UpdateComponent("pipeline_setup", models.ComponentGroupData, "Configuring Pipeline", models.ComponentStatusRunning, "Adding transaction decoder...")
	transactionContextProvider := txtools.NewTransactionContextProvider(a.verbose)
	if err := pipeline.AddProcessor(transactionContextProvider); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add transaction context provider: %w", err))
		return nil, fmt.Errorf("failed to add transaction context provider: %w", err)
	}
	contextProviders = append(contextProviders, transactionContextProvider)

	// Add ABI resolver (runs early - fetches contract ABIs from Etherscan)
	progressTracker.UpdateComponent("pipeline_setup", models.ComponentGroupData, "Configuring Pipeline", models.ComponentStatusRunning, "Adding ABI resolver...")
	abiResolver := txtools.NewABIResolver(a.cache, a.verbose)
	if err := pipeline.AddProcessor(abiResolver); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add ABI resolver: %w", err))
		return nil, fmt.Errorf("failed to add ABI resolver: %w", err)
	}
	contextProviders = append(contextProviders, abiResolver)

	// Add trace decoder (processes trace data to extract calls with ETH transfers)
	progressTracker.UpdateComponent("pipeline_setup", models.ComponentGroupData, "Configuring Pipeline", models.ComponentStatusRunning, "Adding trace decoder...")
	traceDecoder := txtools.NewTraceDecoderWithRPC(client, a.cache, a.verbose)
	if err := pipeline.AddProcessor(traceDecoder); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add trace decoder: %w", err))
		return nil, fmt.Errorf("failed to add trace decoder: %w", err)
	}
	contextProviders = append(contextProviders, traceDecoder)

	// Add log decoder (processes events using resolved ABIs)
	progressTracker.UpdateComponent("pipeline_setup", models.ComponentGroupData, "Configuring Pipeline", models.ComponentStatusRunning, "Adding event decoder...")
	logDecoder := txtools.NewLogDecoderWithRPC(client, a.cache)
	if err := pipeline.AddProcessor(logDecoder); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add log decoder: %w", err))
		return nil, fmt.Errorf("failed to add log decoder: %w", err)
	}
	contextProviders = append(contextProviders, logDecoder)

	// Add signature resolver (4byte.directory lookup for missing signatures)
	progressTracker.UpdateComponent("pipeline_setup", models.ComponentGroupData, "Configuring Pipeline", models.ComponentStatusRunning, "Adding signature resolver...")
	signatureResolver := txtools.NewSignatureResolver(a.cache, a.verbose)
	if err := pipeline.AddProcessor(signatureResolver); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add signature resolver: %w", err))
		return nil, fmt.Errorf("failed to add signature resolver: %w", err)
	}
	contextProviders = append(contextProviders, signatureResolver)

	// Add token transfer extractor (extracts transfers from events)
	progressTracker.UpdateComponent("pipeline_setup", models.ComponentGroupData, "Configuring Pipeline", models.ComponentStatusRunning, "Adding transfer extractor...")
	transferExtractor := txtools.NewTokenTransferExtractor()
	if err := pipeline.AddProcessor(transferExtractor); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add token transfer extractor: %w", err))
		return nil, fmt.Errorf("failed to add token transfer extractor: %w", err)
	}
	contextProviders = append(contextProviders, transferExtractor)

	// Add NFT decoder (extracts and enriches NFT transfers)
	progressTracker.UpdateComponent("pipeline_setup", models.ComponentGroupData, "Configuring Pipeline", models.ComponentStatusRunning, "Adding NFT decoder...")
	nftDecoder := txtools.NewNFTDecoder(a.cache, a.verbose, client)
	if err := pipeline.AddProcessor(nftDecoder); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add NFT decoder: %w", err))
		return nil, fmt.Errorf("failed to add NFT decoder: %w", err)
	}
	contextProviders = append(contextProviders, nftDecoder)

	// Add token metadata enricher
	progressTracker.UpdateComponent("pipeline_setup", models.ComponentGroupData, "Configuring Pipeline", models.ComponentStatusRunning, "Adding token metadata enricher...")
	tokenMetadata := txtools.NewTokenMetadataEnricher(a.cache, a.verbose, client, a.cmcClient)
	if err := pipeline.AddProcessor(tokenMetadata); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add token metadata enricher: %w", err))
		return nil, fmt.Errorf("failed to add token metadata enricher: %w", err)
	}
	contextProviders = append(contextProviders, tokenMetadata)

	// Add amounts finder (NEW - uses LLM to detect ALL relevant amounts generically)
	progressTracker.UpdateComponent("pipeline_setup", models.ComponentGroupData, "Configuring Pipeline", models.ComponentStatusRunning, "Adding AI amounts finder...")
	amountsFinder := txtools.NewAmountsFinder(a.llm, a.verbose)
	if err := pipeline.AddProcessor(amountsFinder); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add amounts finder: %w", err))
		return nil, fmt.Errorf("failed to add amounts finder: %w", err)
	}
	contextProviders = append(contextProviders, amountsFinder)

	// Add icon resolver (discovers token icons from CoinMarketCap + TrustWallet)
	progressTracker.UpdateComponent("pipeline_setup", models.ComponentGroupData, "Configuring Pipeline", models.ComponentStatusRunning, "Adding icon resolver...")
	iconResolver := txtools.NewIconResolver(staticContextProvider, a.cache, a.verbose, a.cmcClient)
	if err := pipeline.AddProcessor(iconResolver); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add icon resolver: %w", err))
		return nil, fmt.Errorf("failed to add icon resolver: %w", err)
	}

	// Add price lookup if API key is available (runs AFTER amounts_finder)
	if a.cmcClient.IsAvailable() {
		progressTracker.UpdateComponent("pipeline_setup", models.ComponentGroupData, "Configuring Pipeline", models.ComponentStatusRunning, "Adding price lookup...")
		priceLookup := txtools.NewERC20PriceLookup(a.cmcClient, a.cache, a.verbose)
		if err := pipeline.AddProcessor(priceLookup); err != nil {
			progressTracker.SendError(fmt.Errorf("failed to add price lookup: %w", err))
			return nil, fmt.Errorf("failed to add price lookup: %w", err)
		}
		contextProviders = append(contextProviders, priceLookup)

		// Add monetary value enricher (runs after amounts_finder + price lookup)
		progressTracker.UpdateComponent("pipeline_setup", models.ComponentGroupData, "Configuring Pipeline", models.ComponentStatusRunning, "Adding monetary enricher...")
		monetaryEnricher := txtools.NewMonetaryValueEnricher(a.llm, a.cmcClient, a.cache, a.verbose)
		if err := pipeline.AddProcessor(monetaryEnricher); err != nil {
			progressTracker.SendError(fmt.Errorf("failed to add monetary value enricher: %w", err))
			return nil, fmt.Errorf("failed to add monetary value enricher: %w", err)
		}
		contextProviders = append(contextProviders, monetaryEnricher)
	}

	// Add ENS resolver (runs after monetary enrichment)
	progressTracker.UpdateComponent("pipeline_setup", models.ComponentGroupData, "Configuring Pipeline", models.ComponentStatusRunning, "Adding ENS resolver...")
	ensResolver := txtools.NewENSResolver(a.cache, a.verbose, client)
	if err := pipeline.AddProcessor(ensResolver); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add ENS resolver: %w", err))
		return nil, fmt.Errorf("failed to add ENS resolver: %w", err)
	}
	contextProviders = append(contextProviders, ensResolver)

	// Add address role resolver (runs after basic data gathering, before analysis tools)
	progressTracker.UpdateComponent("pipeline_setup", models.ComponentGroupData, "Configuring Pipeline", models.ComponentStatusRunning, "Adding address role resolver...")
	addressRoleResolver := txtools.NewAddressRoleResolver(a.llm, a.verbose, client)
	if err := pipeline.AddProcessor(addressRoleResolver); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add address role resolver: %w", err))
		return nil, fmt.Errorf("failed to add address role resolver: %w", err)
	}
	contextProviders = append(contextProviders, addressRoleResolver)

	// Add protocol resolver (probabilistic protocol detection with RAG)
	progressTracker.UpdateComponent("pipeline_setup", models.ComponentGroupData, "Configuring Pipeline", models.ComponentStatusRunning, "Adding protocol resolver...")
	protocolResolver := txtools.NewProtocolResolver(a.llm, a.verbose, 0.6)
	if err := pipeline.AddProcessor(protocolResolver); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add protocol resolver: %w", err))
		return nil, fmt.Errorf("failed to add protocol resolver: %w", err)
	}
	contextProviders = append(contextProviders, protocolResolver)

	// Add tag resolver (probabilistic tag detection with RAG)
	progressTracker.UpdateComponent("pipeline_setup", models.ComponentGroupData, "Configuring Pipeline", models.ComponentStatusRunning, "Adding tag resolver...")
	tagResolver := txtools.NewTagResolver(a.llm, a.verbose, 0.6)
	if err := pipeline.AddProcessor(tagResolver); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add tag resolver: %w", err))
		return nil, fmt.Errorf("failed to add tag resolver: %w", err)
	}
	contextProviders = append(contextProviders, tagResolver)

	// Add context providers to baggage for transaction explainer
	baggage["context_providers"] = contextProviders

	// Add transaction explainer
	progressTracker.UpdateComponent("pipeline_setup", models.ComponentGroupData, "Configuring Pipeline", models.ComponentStatusRunning, "Adding AI explainer...")
	if err := pipeline.AddProcessor(a.explainer); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add transaction explainer: %w", err))
		return nil, fmt.Errorf("failed to add transaction explainer: %w", err)
	}

	// Add annotation generator (runs after explanation is generated)
	progressTracker.UpdateComponent("pipeline_setup", models.ComponentGroupData, "Configuring Pipeline", models.ComponentStatusRunning, "Adding annotation generator...")
	annotationGenerator := txtools.NewAnnotationGenerator(a.llm, a.verbose)
	if err := pipeline.AddProcessor(annotationGenerator); err != nil {
		progressTracker.SendError(fmt.Errorf("failed to add annotation generator: %w", err))
		return nil, fmt.Errorf("failed to add annotation generator: %w", err)
	}

	// Complete pipeline setup
	progressTracker.UpdateComponent("pipeline_setup", models.ComponentGroupData, "Configuring Pipeline", models.ComponentStatusFinished, fmt.Sprintf("Pipeline ready with %d tools", pipeline.GetProcessorCount()))

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
			// For security, exclude raw RPC responses and potentially sensitive debug data
			switch key {
			case "raw_data":
				// Don't include raw blockchain data in metadata - it's large and unnecessary
				cleanBaggage[key] = "<raw_data - excluded for security and size>"
			case "rpc_responses", "api_responses", "debug_info":
				// Exclude any RPC or API responses that might contain endpoint info
				cleanBaggage[key] = fmt.Sprintf("<%s - excluded for security>", key)
			default:
				// Include other metadata but ensure no sensitive data
				cleanBaggage[key] = value
			}
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
