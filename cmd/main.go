package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/txplain/txplain/internal/agent"
	"github.com/txplain/txplain/internal/api"
	"github.com/txplain/txplain/internal/mcp"
	"github.com/txplain/txplain/internal/models"
	"github.com/txplain/txplain/internal/rpc"
)

func main() {
	// Load .env file if it exists
	if err := godotenv.Load(); err != nil {
		// Don't fail if .env doesn't exist, just log
		log.Printf("No .env file found or error loading it: %v", err)
	}

	// Initialize networks from environment variables
	models.InitializeNetworks()

	// Command line flags
	var (
		httpAddr      = flag.String("http-addr", ":8080", "HTTP server address")
		mcpAddr       = flag.String("mcp-addr", ":8081", "MCP server address")
		openaiKey     = flag.String("openai-key", "", "OpenAI API key (can also be set via OPENAI_API_KEY env var)")
		coinMarketKey = flag.String("cmc-key", "", "CoinMarketCap API key (can also be set via COINMARKETCAP_API_KEY env var)")
		enableHTTP    = flag.Bool("http", true, "Enable HTTP API server")
		enableMCP     = flag.Bool("mcp", false, "Enable MCP server")
		showVersion   = flag.Bool("version", false, "Show version and exit")
		verbose       = flag.Bool("v", false, "Verbose mode - show prompts sent to LLM. Set DEBUG=true env var to also show baggage debug info")
		debugToken    = flag.String("debug-token", "", "Debug specific token contract (address)")
		txHash        = flag.String("tx", "", "Transaction hash to explain")
		networkID     = flag.Int64("network", 1, "Network ID (1=Ethereum, 137=Polygon, 42161=Arbitrum)")
	)
	flag.Parse()

	// Show version and exit if requested
	if *showVersion {
		fmt.Println("Txplain v1.0.0")
		fmt.Println("AI-powered blockchain transaction explanation service")
		os.Exit(0)
	}

	// Handle debug token mode
	if *debugToken != "" {
		debugTokenContract(*debugToken, *networkID)
		return
	}

	// Check if transaction hash is provided
	if *txHash != "" {
		// Transaction explanation mode
		explainTransaction(*txHash, *networkID, *openaiKey, *coinMarketKey, *verbose)
		return
	}

	// Server mode (original functionality)
	runServers(*httpAddr, *mcpAddr, *openaiKey, *coinMarketKey, *enableHTTP, *enableMCP)
}

// explainTransaction processes a single transaction and prints the explanation
func explainTransaction(txHash string, networkID int64, openaiKey string, coinMarketKey string, verbose bool) {
	// Validate transaction hash format
	if !strings.HasPrefix(txHash, "0x") || len(txHash) != 66 {
		log.Fatal("Invalid transaction hash format. Expected format: 0x followed by 64 hex characters")
	}

	// Validate network ID
	if !models.IsValidNetwork(networkID) {
		log.Fatal("Unsupported network ID. Supported networks: 1 (Ethereum), 137 (Polygon), 42161 (Arbitrum)")
	}

	// Get OpenAI API key
	apiKey := openaiKey
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	if apiKey == "" {
		log.Fatal("OpenAI API key is required. Set OPENAI_API_KEY environment variable or use -openai-key flag")
	}

	// Get CoinMarketCap API key
	cmcKey := coinMarketKey
	if cmcKey == "" {
		cmcKey = os.Getenv("COINMARKETCAP_API_KEY")
	}

	if verbose {
		fmt.Printf("🔍 Analyzing transaction: %s\n", txHash)
	}
	network, _ := models.GetNetwork(networkID)
	if verbose {
		fmt.Printf("🌐 Network: %s (%d)\n", network.Name, networkID)
		fmt.Printf("🔗 Explorer: %s/tx/%s\n\n", network.Explorer, txHash)
	}

	// Create the agent
	txAgent, err := agent.NewTxplainAgent(apiKey, cmcKey)
	if err != nil {
		log.Fatalf("Failed to initialize agent: %v", err)
	}

	// Enable verbose mode if requested
	if verbose {
		txAgent.SetVerbose(true)
	}

	// Create transaction request
	request := &models.TransactionRequest{
		TxHash:    txHash,
		NetworkID: networkID,
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if verbose {
		fmt.Println("⏳ Fetching transaction data from blockchain...")
	}

	// Process the transaction
	result, err := txAgent.ExplainTransaction(ctx, request)
	if err != nil {
		log.Fatalf("Failed to explain transaction: %v", err)
	}

	// Print debug information if verbose
	if os.Getenv("DEBUG") == "true" {
		fmt.Println("\n" + strings.Repeat("=", 80))
		fmt.Println("🔧 DEBUG INFORMATION")
		fmt.Println(strings.Repeat("=", 80))

		// Show baggage contents (metadata)
		if result.Metadata != nil {
			if baggage, ok := result.Metadata["pipeline_baggage"].(map[string]interface{}); ok {

				// First, display formatted debug information if available
				if debugInfo, ok := baggage["debug_info"].(map[string]interface{}); ok {
					// Display token metadata debug
					if tokenDebug, ok := debugInfo["token_metadata"].(map[string]interface{}); ok {
						fmt.Println("=== TOKEN METADATA DEBUG ===")
						if discoveredAddresses, ok := tokenDebug["discovered_addresses"].([]string); ok {
							fmt.Printf("Discovered %d token addresses: %v\n", len(discoveredAddresses), discoveredAddresses)
						}
						if rpcResults, ok := tokenDebug["rpc_results"].([]string); ok {
							fmt.Println("RPC Results:")
							for _, result := range rpcResults {
								fmt.Println("  - " + result)
							}
						}
						if rpcErrors, ok := tokenDebug["token_metadata_rpc_errors"].([]string); ok {
							fmt.Println("RPC Errors:")
							for _, err := range rpcErrors {
								fmt.Println("  - " + err)
							}
						}
						if finalMetadata, ok := tokenDebug["final_metadata"].([]string); ok {
							fmt.Println("Final Metadata:")
							for _, metadata := range finalMetadata {
								fmt.Println("  - " + metadata)
							}
						}
						fmt.Println()
					}

					// Display transfer enrichment debug
					if transferDebug, ok := debugInfo["transfer_enrichment"].([]string); ok {
						fmt.Println("=== TRANSFER ENRICHMENT DEBUG ===")
						for _, debug := range transferDebug {
							fmt.Println("  - " + debug)
						}
						fmt.Println()
					}
				}

				fmt.Println("📦 Pipeline Baggage Contents:")

				// Create a safe copy of baggage to avoid circular references
				safeBaggage := make(map[string]interface{})
				for key, value := range baggage {
					// Skip the "explanation" field as it contains circular references
					if key == "explanation" {
						safeBaggage[key] = "[ExplanationResult - excluded to prevent circular reference]"
					} else {
						safeBaggage[key] = value
					}
				}

				baggageJSON, err := json.MarshalIndent(safeBaggage, "", "  ")
				if err != nil {
					fmt.Printf("Error marshaling baggage to JSON: %v\n", err)
					// Fallback to basic key listing
					fmt.Println("Available keys:")
					for key := range baggage {
						fmt.Printf("- %s\n", key)
					}
				} else {
					fmt.Println(string(baggageJSON))
				}
				fmt.Println()
			}
		}
	}

	// Print the explanation
	printTransactionExplanation(result, verbose)
}

// printTransactionExplanation formats and prints the transaction explanation
func printTransactionExplanation(result *models.ExplanationResult, verbose bool) {
	if verbose {
		// Verbose output with full details
		fmt.Println("\n" + strings.Repeat("=", 80))
		fmt.Println("📊 TRANSACTION EXPLANATION")
		fmt.Println(strings.Repeat("=", 80))

		// Basic transaction info
		fmt.Printf("📝 Transaction Hash: %s\n", result.TxHash)
		fmt.Printf("🏷️  Status: %s\n", strings.ToUpper(result.Status))
		fmt.Printf("⛽ Gas Used: %s\n", formatNumber(result.GasUsed))
		if result.BlockNumber > 0 {
			fmt.Printf("📦 Block: %s\n", formatNumber(result.BlockNumber))
		}
		fmt.Println()

		// AI Summary
		if result.Summary != "" {
			fmt.Println("🧠 AI EXPLANATION")
			fmt.Println(strings.Repeat("-", 50))
			fmt.Println(result.Summary)
			fmt.Println()
		}

		// Explorer link
		if len(result.Links) > 0 {
			if txLink, exists := result.Links["transaction"]; exists {
				fmt.Printf("🔗 View on Explorer: %s\n", txLink)
				fmt.Println()
			}
		}

		fmt.Println(strings.Repeat("=", 80))
		fmt.Println("✅ Analysis complete!")
	} else {
		// Simple output - just the AI summary
		if result.Summary != "" {
			fmt.Println(result.Summary)
		} else {
			fmt.Println("Transaction processed but no explanation generated.")
		}
	}
}

// formatNumber formats large numbers with commas
func formatNumber(n uint64) string {
	str := fmt.Sprintf("%d", n)
	if len(str) < 4 {
		return str
	}

	// Add commas every 3 digits from right
	result := ""
	for i, char := range str {
		if i > 0 && (len(str)-i)%3 == 0 {
			result += ","
		}
		result += string(char)
	}
	return result
}

// runServers runs the original server functionality
func runServers(httpAddr, mcpAddr, openaiKey, coinMarketKey string, enableHTTP, enableMCP bool) {
	// Get OpenAI API key
	apiKey := openaiKey
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	if apiKey == "" {
		log.Fatal("OpenAI API key is required. Set OPENAI_API_KEY environment variable or use -openai-key flag")
	}

	// Get CoinMarketCap API key
	cmcKey := coinMarketKey
	if cmcKey == "" {
		cmcKey = os.Getenv("COINMARKETCAP_API_KEY")
	}

	// Channel to listen for interrupt signal
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	// Variables to hold server instances
	var httpServer *api.Server
	var mcpServer *mcp.Server

	// Start servers
	errChan := make(chan error, 2)

	// Start HTTP API server if enabled
	if enableHTTP {
		server, err := api.NewServer(httpAddr, apiKey, cmcKey)
		if err != nil {
			log.Fatalf("Failed to create HTTP server: %v", err)
		}
		httpServer = server

		go func() {
			log.Printf("Starting Txplain API server on %s", httpAddr)
			if err := server.Start(); err != nil {
				errChan <- fmt.Errorf("HTTP server error: %w", err)
			}
		}()
	}

	// Start MCP server if enabled
	if enableMCP {
		server, err := mcp.NewServer(mcpAddr, apiKey, cmcKey)
		if err != nil {
			log.Fatalf("Failed to create MCP server: %v", err)
		}
		mcpServer = server

		go func() {
			log.Printf("Starting Txplain MCP server on %s", mcpAddr)
			if err := server.Start(); err != nil {
				errChan <- fmt.Errorf("MCP server error: %w", err)
			}
		}()
	}

	// Log startup completion
	log.Println("Txplain service started successfully")
	log.Println("Supported networks: Ethereum (1), Polygon (137), Arbitrum (42161)")

	if enableHTTP {
		log.Printf("HTTP API endpoints:")
		log.Printf("  Health: http://localhost%s/health", httpAddr)
		log.Printf("  Networks: http://localhost%s/api/v1/networks", httpAddr)
		log.Printf("  Explain: POST http://localhost%s/api/v1/explain", httpAddr)
	}

	if enableMCP {
		log.Printf("MCP server available at: http://localhost%s", mcpAddr)
	}

	// Wait for shutdown signal or error
	select {
	case sig := <-signalChan:
		log.Printf("Received signal: %v", sig)
	case err := <-errChan:
		log.Printf("Server error: %v", err)
	}

	// Graceful shutdown
	log.Println("Shutting down servers...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	// Shutdown HTTP server
	if httpServer != nil {
		if err := httpServer.Stop(shutdownCtx); err != nil {
			log.Printf("Error shutting down HTTP server: %v", err)
		}
	}

	// Shutdown MCP server
	if mcpServer != nil {
		if err := mcpServer.Stop(shutdownCtx); err != nil {
			log.Printf("Error shutting down MCP server: %v", err)
		}
	}

	log.Println("Shutdown completed")
}

// debugTokenContract debugs a specific token contract
func debugTokenContract(contractAddress string, networkID int64) {
	ctx := context.Background()

	fmt.Printf("=== DEBUGGING TOKEN CONTRACT ===\n")
	fmt.Printf("Contract: %s\n", contractAddress)
	fmt.Printf("Network: %d\n", networkID)
	fmt.Printf("\n")

	// Create RPC client
	client, err := rpc.NewClient(networkID)
	if err != nil {
		fmt.Printf("ERROR: Failed to create RPC client: %v\n", err)
		return
	}

	// Get contract info directly via RPC
	contractInfo, err := client.GetContractInfo(ctx, contractAddress)
	if err != nil {
		fmt.Printf("ERROR: Failed to get contract info: %v\n", err)
		return
	}

	// Print results
	fmt.Printf("=== RESULTS ===\n")
	fmt.Printf("Address: %s\n", contractInfo.Address)
	fmt.Printf("Type: %s\n", contractInfo.Type)
	fmt.Printf("Name: %s\n", contractInfo.Name)
	fmt.Printf("Symbol: %s\n", contractInfo.Symbol)
	fmt.Printf("Decimals: %d\n", contractInfo.Decimals)
	fmt.Printf("Total Supply: %s\n", contractInfo.TotalSupply)

	if len(contractInfo.Metadata) > 0 {
		fmt.Printf("Metadata:\n")
		for key, value := range contractInfo.Metadata {
			fmt.Printf("  %s: %v\n", key, value)
		}
	}
}
