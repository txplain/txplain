package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/txplain/txplain/internal/agent"
	"github.com/txplain/txplain/internal/models"
)

// Server represents the API server
type Server struct {
	router   *mux.Router
	agent    *agent.TxplainAgent
	address  string
	server   *http.Server
}

// NewServer creates a new API server
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

// setupRoutes configures all the API routes
func (s *Server) setupRoutes() {
	// Add CORS middleware
	s.router.Use(s.corsMiddleware)
	s.router.Use(s.loggingMiddleware)

	// Health check endpoint
	s.router.HandleFunc("/health", s.handleHealth).Methods("GET")

	// API version 1
	v1 := s.router.PathPrefix("/api/v1").Subrouter()
	
	// Transaction explanation endpoint
	v1.HandleFunc("/explain", s.handleExplainTransaction).Methods("POST")
	
	// Get supported networks
	v1.HandleFunc("/networks", s.handleGetNetworks).Methods("GET")

	// Transaction details (without explanation)
	v1.HandleFunc("/transaction/{network}/{hash}", s.handleGetTransactionDetails).Methods("GET")
}

// handleHealth returns the health status of the service
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	response := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().UTC(),
		"service":   "txplain",
		"version":   "1.0.0",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// handleExplainTransaction handles transaction explanation requests
func (s *Server) handleExplainTransaction(w http.ResponseWriter, r *http.Request) {
	// Parse request body
	var request models.TransactionRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "Invalid request body", err)
		return
	}

	// Validate request
	if request.TxHash == "" {
		s.writeErrorResponse(w, http.StatusBadRequest, "Transaction hash is required", nil)
		return
	}

	if !models.IsValidNetwork(request.NetworkID) {
		s.writeErrorResponse(w, http.StatusBadRequest, "Unsupported network ID", nil)
		return
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// Process the transaction
	explanation, err := s.agent.ExplainTransaction(ctx, &request)
	if err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "Failed to explain transaction", err)
		return
	}

	// Return the explanation
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(explanation)
}

// handleGetNetworks returns the list of supported networks
func (s *Server) handleGetNetworks(w http.ResponseWriter, r *http.Request) {
	networks := s.agent.GetSupportedNetworks()
	
	// Convert to array for better API response
	var networkList []models.Network
	for _, network := range networks {
		networkList = append(networkList, network)
	}

	response := map[string]interface{}{
		"networks": networkList,
		"count":    len(networkList),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// handleGetTransactionDetails returns basic transaction details without full explanation
func (s *Server) handleGetTransactionDetails(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	networkIDStr := vars["network"]
	txHash := vars["hash"]

	// Parse network ID
	networkID, err := strconv.ParseInt(networkIDStr, 10, 64)
	if err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "Invalid network ID", err)
		return
	}

	// Validate network
	if !models.IsValidNetwork(networkID) {
		s.writeErrorResponse(w, http.StatusBadRequest, "Unsupported network ID", nil)
		return
	}

	// This is a placeholder for transaction details endpoint
	// In a full implementation, this would fetch basic transaction data without the AI explanation
	response := map[string]interface{}{
		"tx_hash":    txHash,
		"network_id": networkID,
		"message":    "Transaction details endpoint - not fully implemented yet",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// writeErrorResponse writes an error response in a consistent format
func (s *Server) writeErrorResponse(w http.ResponseWriter, statusCode int, message string, err error) {
	response := map[string]interface{}{
		"error":     message,
		"timestamp": time.Now().UTC(),
	}

	if err != nil {
		// Only include detailed error in development
		if os.Getenv("ENV") == "development" {
			response["details"] = err.Error()
		}
		log.Printf("API Error: %s - %v", message, err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(response)
}

// corsMiddleware adds CORS headers
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		// Handle preflight requests
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// loggingMiddleware logs HTTP requests
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		
		// Wrap the ResponseWriter to capture status code
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		
		next.ServeHTTP(wrapped, r)
		
		duration := time.Since(start)
		log.Printf("[%s] %s %s - %d (%v)",
			r.Method,
			r.RequestURI,
			r.RemoteAddr,
			wrapped.statusCode,
			duration,
		)
	})
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// Start starts the HTTP server
func (s *Server) Start() error {
	s.server = &http.Server{
		Addr:    s.address,
		Handler: s.router,
		
		// Security settings
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second, // Long timeout for AI processing
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("Starting Txplain API server on %s", s.address)
	return s.server.ListenAndServe()
}

// Stop gracefully stops the HTTP server
func (s *Server) Stop(ctx context.Context) error {
	log.Println("Shutting down Txplain API server...")
	
	if s.server != nil {
		if err := s.server.Shutdown(ctx); err != nil {
			return fmt.Errorf("failed to shutdown server: %w", err)
		}
	}

	// Close agent resources
	if s.agent != nil {
		s.agent.Close()
	}

	return nil
} 