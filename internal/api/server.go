package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/txplain/txplain/internal/agent"
	"github.com/txplain/txplain/internal/models"
	"github.com/txplain/txplain/internal/tools"
)

// Server represents the API server
type Server struct {
	router  *mux.Router
	agent   *agent.TxplainAgent
	address string
	server  *http.Server
}

// NewServer creates a new API server
func NewServer(address string, openaiAPIKey string, cache tools.Cache, verbose bool) (*Server, error) {
	// Initialize the Txplain agent
	txAgent, err := agent.NewTxplainAgent(openaiAPIKey, cache, verbose)
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
	s.router.Use(s.recoveryMiddleware) // Add recovery middleware to catch panics
	s.router.Use(s.loggingMiddleware)

	// Health check endpoint
	s.router.HandleFunc("/health", s.handleHealth).Methods("GET")

	// API version 1
	v1 := s.router.PathPrefix("/api/v1").Subrouter()

	// Transaction explanation endpoint
	v1.HandleFunc("/explain", s.handleExplainTransaction).Methods("POST")

	// Transaction explanation with Server-Sent Events
	v1.HandleFunc("/explain-sse", s.handleExplainTransactionSSE).Methods("POST")

	// Get supported networks
	v1.HandleFunc("/networks", s.handleGetNetworks).Methods("GET")

	// Transaction details (without explanation)
	v1.HandleFunc("/transaction/{network}/{hash}", s.handleGetTransactionDetails).Methods("GET")

	// Serve static assets (CSS, JS, etc.) - must come before SPA handler
	s.router.PathPrefix("/assets/").Handler(http.StripPrefix("/assets/", http.FileServer(http.Dir("./web/dist/assets/"))))

	// Serve vite.svg and other root-level static files
	s.router.HandleFunc("/vite.svg", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./web/dist/vite.svg")
	})

	// Serve index.html for SPA routing (must be last to catch all remaining routes)
	s.router.PathPrefix("/").HandlerFunc(s.handleSPA).Methods("GET")
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
	ctx, cancel := context.WithTimeout(r.Context(), 300*time.Second)
	defer cancel()

	// Process the transaction
	log.Printf("Starting transaction analysis for %s on network %d", request.TxHash, request.NetworkID)
	explanation, err := s.agent.ExplainTransaction(ctx, &request)
	if err != nil {
		log.Printf("ExplainTransaction failed: %v", err)
		// Check for specific error types to provide better error messages
		if ctx.Err() == context.DeadlineExceeded {
			s.writeErrorResponse(w, http.StatusRequestTimeout, "Transaction analysis timed out after 300 seconds", err)
		} else if ctx.Err() == context.Canceled {
			s.writeErrorResponse(w, http.StatusRequestTimeout, "Request was canceled", err)
		} else if strings.Contains(err.Error(), "context canceled") {
			s.writeErrorResponse(w, http.StatusRequestTimeout, "Request timed out during processing", err)
		} else {
			s.writeErrorResponse(w, http.StatusInternalServerError, "Failed to explain transaction", err)
		}
		return
	}

	log.Printf("ExplainTransaction succeeded, explanation summary length: %d chars", len(explanation.Summary))
	log.Printf("Explanation has %d transfers", len(explanation.Transfers))

	// Return the explanation
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	// Create a debug version without metadata to avoid potential circular references
	debugExplanation := *explanation
	debugExplanation.Metadata = nil

	log.Printf("About to encode JSON response...")

	// Ensure we handle JSON encoding errors
	if encodeErr := json.NewEncoder(w).Encode(explanation); encodeErr != nil {
		log.Printf("FAILED to encode explanation response as JSON: %v", encodeErr)
		log.Printf("Explanation object: %+v", debugExplanation)
		// At this point we've already sent 200 status, so we can't change it
		// but we can log the error for debugging
	} else {
		log.Printf("Successfully encoded and sent JSON response")
	}
}

// handleExplainTransactionSSE handles transaction explanation with Server-Sent Events
func (s *Server) handleExplainTransactionSSE(w http.ResponseWriter, r *http.Request) {
	// Parse request body FIRST before any SSE setup
	var request models.TransactionRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		// Since we haven't set SSE headers yet, we can return a regular HTTP error
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

	// Now set SSE headers with additional anti-buffering configuration
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate, private")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Cache-Control")
	// Additional headers to prevent buffering
	w.Header().Set("X-Accel-Buffering", "no") // Nginx
	w.Header().Set("X-Proxy-Buffering", "no") // Generic proxy
	w.Header().Set("Proxy-Buffering", "off")  // Apache/other proxies
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("Transfer-Encoding", "chunked")

	// Immediately write status to establish connection
	w.WriteHeader(http.StatusOK)

	// Force immediate flush helper with padding
	forceFlushWithPadding := func() {
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		// Add padding to ensure packet size exceeds buffer thresholds
		padding := strings.Repeat(" ", 1024) // 1KB padding
		fmt.Fprintf(w, ": padding-%d %s\n\n", time.Now().UnixNano(), padding)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}

	// Send multiple large immediate pings to establish connection and prevent buffering
	timestamp := time.Now().UnixNano()
	largePadding := strings.Repeat("x", 2048) // 2KB of data to force immediate transmission
	fmt.Fprintf(w, ": connection-established-%d %s\n\n", timestamp, largePadding)
	forceFlushWithPadding()
	time.Sleep(50 * time.Millisecond)

	fmt.Fprintf(w, ": anti-buffer-ping-%d %s\n\n", time.Now().UnixNano(), largePadding)
	forceFlushWithPadding()
	time.Sleep(50 * time.Millisecond)

	fmt.Fprintf(w, ": ready-for-events-%d %s\n\n", time.Now().UnixNano(), largePadding)
	forceFlushWithPadding()

	// Create unbuffered progress channel
	progressChan := make(chan models.ProgressEvent)

	// Start aggressive anti-buffer goroutine
	antiBufferCtx, antiBufferCancel := context.WithCancel(r.Context())
	defer antiBufferCancel()

	go func() {
		ticker := time.NewTicker(200 * time.Millisecond) // Very frequent
		defer ticker.Stop()
		counter := 0

		for {
			select {
			case <-antiBufferCtx.Done():
				return
			case <-ticker.C:
				counter++
				// Send timestamp with large payload to force transmission
				largePing := strings.Repeat("a", 512)
				fmt.Fprintf(w, ": heartbeat-%d-%d %s\n\n", time.Now().UnixNano(), counter, largePing)
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
			}
		}
	}()

	// Start processing in goroutine
	go func() {
		defer close(progressChan)

		result, err := s.agent.ExplainTransactionWithProgress(r.Context(), &request, progressChan)
		if err != nil {
			// CRITICAL FIX: Ensure error is sent to client before channel closes
			// Previously, this was causing components to get stuck when errors occurred
			select {
			case progressChan <- models.ProgressEvent{
				Type:      "error",
				Error:     fmt.Sprintf("Transaction analysis failed: %v", err),
				Timestamp: time.Now(),
			}:
				// Error sent successfully
			case <-time.After(1 * time.Second):
				// Timeout sending error, proceed to close channel
				// This prevents hanging if the client has disconnected
			}
			return
		}

		// Send final completion (backup in case agent doesn't send it)
		progressChan <- models.ProgressEvent{
			Type:      "complete",
			Result:    result,
			Timestamp: time.Now(),
		}
	}()

	// Stream progress updates with immediate flushing
	eventCount := 0

	for event := range progressChan {
		eventCount++
		serverTime := time.Now()

		// Log server-side timing for debugging
		// log.Printf("[SSE-DEBUG] Sending event %d at %v: %s", eventCount, serverTime, event.Type)

		eventData, err := json.Marshal(event)
		if err != nil {
			continue // Skip malformed events
		}

		// Send SSE event with timestamp and immediate flush
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, eventData)
		forceFlushWithPadding()

		// Send a duplicate event with slight variation to force network transmission
		if event.Type == "component_update" {
			duplicateData := fmt.Sprintf(`{"duplicate":true,"original_time":"%v","server_time":"%v","event_data":%s}`,
				event.Timestamp, serverTime, string(eventData))
			fmt.Fprintf(w, ": duplicate-%d %s\n\n", eventCount, duplicateData)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}

		// Log successful send
		// log.Printf("[SSE-DEBUG] Event %d sent and flushed at %v", eventCount, time.Now())

		// Break on completion or error
		if event.Type == "complete" || event.Type == "error" {
			break
		}
	}

	// Final flush with confirmation
	// log.Printf("[SSE-DEBUG] Stream completed, sending final flush")
	forceFlushWithPadding()
}

// handleGetNetworks returns the list of supported networks
func (s *Server) handleGetNetworks(w http.ResponseWriter, r *http.Request) {
	networks := s.agent.GetSupportedNetworks()

	// Convert to public network list (excludes sensitive RPC URLs)
	var networkList []models.PublicNetwork
	for _, network := range networks {
		networkList = append(networkList, network.ToPublic())
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

// handleSPA serves the React SPA for non-API routes
func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	// Skip API routes and static assets
	if strings.HasPrefix(r.URL.Path, "/api/") ||
		strings.HasPrefix(r.URL.Path, "/health") ||
		strings.HasPrefix(r.URL.Path, "/assets/") ||
		r.URL.Path == "/vite.svg" {
		http.NotFound(w, r)
		return
	}

	// Serve index.html for SPA routing
	http.ServeFile(w, r, "./web/dist/index.html")
}

// writeErrorResponse writes an error response in a consistent format
func (s *Server) writeErrorResponse(w http.ResponseWriter, statusCode int, message string, err error) {
	response := map[string]interface{}{
		"error":     message,
		"timestamp": time.Now().UTC(),
	}

	if err != nil {
		// Log the full error details for debugging (logs are private)
		log.Printf("API Error: %s - %v", message, err)

		// For security, do NOT expose full error details in public API responses
		// Only include sanitized error information that doesn't leak sensitive data
		switch {
		case strings.Contains(err.Error(), "RPC"):
			response["details"] = "Network connectivity issue"
		case strings.Contains(err.Error(), "API"):
			response["details"] = "External service error"
		case strings.Contains(err.Error(), "failed to initialize"):
			response["details"] = "Service initialization error"
		case strings.Contains(err.Error(), "context"):
			response["details"] = "Request timeout"
		default:
			// Generic error message that doesn't leak internal details
			response["details"] = "Internal processing error"
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	// Ensure we always write a response, even if JSON encoding fails
	if encodeErr := json.NewEncoder(w).Encode(response); encodeErr != nil {
		log.Printf("Failed to encode error response as JSON: %v", encodeErr)
		// Can't call WriteHeader again, just write fallback JSON
		w.Write([]byte(`{"error":"Internal server error - failed to encode response"}`))
	}
}

// recoveryMiddleware catches panics and returns proper JSON error responses
func (s *Server) recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("PANIC in %s %s: %v", r.Method, r.URL.Path, err)

				// Only write response if headers haven't been sent yet
				if w.Header().Get("Content-Type") == "" {
					s.writeErrorResponse(w, http.StatusInternalServerError, "Internal server error", fmt.Errorf("panic: %v", err))
				} else {
					// Headers already sent, just log the error
					log.Printf("Cannot send error response, headers already sent")
				}
			}
		}()

		next.ServeHTTP(w, r)
	})
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

		// Security settings optimized for SSE streaming
		ReadTimeout:       300 * time.Second, // 5 minutes for complex requests
		WriteTimeout:      300 * time.Second, // Long timeout for AI processing
		IdleTimeout:       300 * time.Second, // 5 minutes idle timeout
		ReadHeaderTimeout: 30 * time.Second,  // Prevent slow header attacks

		// Disable HTTP/2 for better SSE compatibility if needed
		// TLSNextProto: make(map[string]func(*http.Server, *tls.Conn, http.Handler)),
	}

	log.Printf("Starting Txplain API server on %s", s.address)
	log.Printf("SSE buffering optimizations enabled")
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
