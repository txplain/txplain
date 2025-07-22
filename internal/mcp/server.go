package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/txplain/txplain/internal/agent"
	"github.com/txplain/txplain/internal/models"
)

// Server represents the MCP server
type Server struct {
	router  *mux.Router
	agent   *agent.TxplainAgent
	address string
	server  *http.Server
}

// MCPRequest represents a Model Context Protocol request
type MCPRequest struct {
	Method string                 `json:"method"`
	Params map[string]interface{} `json:"params"`
	ID     interface{}            `json:"id"`
}

// MCPResponse represents a Model Context Protocol response
type MCPResponse struct {
	Result interface{} `json:"result,omitempty"`
	Error  *MCPError   `json:"error,omitempty"`
	ID     interface{} `json:"id"`
}

// MCPError represents an MCP error
type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

// NewServer creates a new MCP server
func NewServer(address string, openaiAPIKey string, coinMarketCapAPIKey string) (*Server, error) {
	// Initialize the Txplain agent
	txAgent, err := agent.NewTxplainAgent(openaiAPIKey, coinMarketCapAPIKey)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize agent: %w", err)
	}

	server := &Server{
		router:  mux.NewRouter(),
		agent:   txAgent,
		address: address,
	}

	server.setupRoutes()

	return server, nil
}

// setupRoutes configures the MCP server routes
func (s *Server) setupRoutes() {
	s.router.Use(s.loggingMiddleware)

	// MCP endpoint
	s.router.HandleFunc("/mcp", s.handleMCPRequest).Methods("POST")

	// Health check
	s.router.HandleFunc("/health", s.handleHealth).Methods("GET")

	// Capabilities endpoint
	s.router.HandleFunc("/capabilities", s.handleCapabilities).Methods("GET")
}

// handleMCPRequest handles Model Context Protocol requests
func (s *Server) handleMCPRequest(w http.ResponseWriter, r *http.Request) {
	var request MCPRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		s.writeErrorResponse(w, request.ID, -32700, "Parse error", err.Error())
		return
	}

	// Route based on method
	switch request.Method {
	case "txplain.explain":
		s.handleExplainMethod(w, &request)
	case "txplain.networks":
		s.handleNetworksMethod(w, &request)
	case "txplain.capabilities":
		s.handleCapabilitiesMethod(w, &request)
	default:
		s.writeErrorResponse(w, request.ID, -32601, "Method not found", fmt.Sprintf("Unknown method: %s", request.Method))
	}
}

// handleExplainMethod handles transaction explanation requests via MCP
func (s *Server) handleExplainMethod(w http.ResponseWriter, request *MCPRequest) {
	// Extract parameters
	txHash, ok := request.Params["tx_hash"].(string)
	if !ok {
		s.writeErrorResponse(w, request.ID, -32602, "Invalid params", "tx_hash is required")
		return
	}

	networkIDFloat, ok := request.Params["network_id"].(float64)
	if !ok {
		s.writeErrorResponse(w, request.ID, -32602, "Invalid params", "network_id is required")
		return
	}
	networkID := int64(networkIDFloat)

	// Validate network
	if !models.IsValidNetwork(networkID) {
		s.writeErrorResponse(w, request.ID, -32602, "Invalid params", "Unsupported network ID")
		return
	}

	// Create transaction request
	txRequest := &models.TransactionRequest{
		TxHash:    txHash,
		NetworkID: networkID,
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Process the transaction
	explanation, err := s.agent.ExplainTransaction(ctx, txRequest)
	if err != nil {
		s.writeErrorResponse(w, request.ID, -32000, "Processing error", err.Error())
		return
	}

	// Write successful response
	response := MCPResponse{
		Result: explanation,
		ID:     request.ID,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// handleNetworksMethod handles network list requests
func (s *Server) handleNetworksMethod(w http.ResponseWriter, request *MCPRequest) {
	networks := s.agent.GetSupportedNetworks()

	// Convert to array
	var networkList []models.Network
	for _, network := range networks {
		networkList = append(networkList, network)
	}

	response := MCPResponse{
		Result: map[string]interface{}{
			"networks": networkList,
			"count":    len(networkList),
		},
		ID: request.ID,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// handleCapabilitiesMethod handles capabilities requests
func (s *Server) handleCapabilitiesMethod(w http.ResponseWriter, request *MCPRequest) {
	capabilities := map[string]interface{}{
		"service": "txplain",
		"version": "1.0.0",
		"methods": []string{
			"txplain.explain",
			"txplain.networks",
			"txplain.capabilities",
		},
		"supported_networks": []map[string]interface{}{
			{"id": 1, "name": "Ethereum"},
			{"id": 137, "name": "Polygon"},
			{"id": 42161, "name": "Arbitrum"},
		},
		"features": []string{
			"transaction_analysis",
			"human_readable_explanations",
			"multi_network_support",
			"token_transfer_detection",
			"defi_protocol_recognition",
		},
	}

	response := MCPResponse{
		Result: capabilities,
		ID:     request.ID,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// handleHealth returns health status
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	response := map[string]interface{}{
		"status":    "healthy",
		"service":   "txplain-mcp",
		"timestamp": time.Now().UTC(),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// handleCapabilities returns service capabilities
func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	capabilities := map[string]interface{}{
		"service": "txplain",
		"version": "1.0.0",
		"methods": []string{
			"txplain.explain",
			"txplain.networks",
			"txplain.capabilities",
		},
		"supported_networks": s.agent.GetSupportedNetworks(),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(capabilities)
}

// writeErrorResponse writes an MCP error response
func (s *Server) writeErrorResponse(w http.ResponseWriter, id interface{}, code int, message, data string) {
	response := MCPResponse{
		Error: &MCPError{
			Code:    code,
			Message: message,
			Data:    data,
		},
		ID: id,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK) // MCP errors are still HTTP 200
	json.NewEncoder(w).Encode(response)
}

// loggingMiddleware logs MCP requests
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		duration := time.Since(start)
		log.Printf("[MCP] %s %s - (%v)", r.Method, r.RequestURI, duration)
	})
}

// Start starts the MCP server
func (s *Server) Start() error {
	s.server = &http.Server{
		Addr:         s.address,
		Handler:      s.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("Starting Txplain MCP server on %s", s.address)
	return s.server.ListenAndServe()
}

// Stop gracefully stops the MCP server
func (s *Server) Stop(ctx context.Context) error {
	log.Println("Shutting down Txplain MCP server...")

	if s.server != nil {
		if err := s.server.Shutdown(ctx); err != nil {
			return fmt.Errorf("failed to shutdown MCP server: %w", err)
		}
	}

	// Close agent resources
	if s.agent != nil {
		s.agent.Close()
	}

	return nil
}
