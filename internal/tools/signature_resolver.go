package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/txplain/txplain/internal/models"
)

// SignatureResolver resolves function and event signatures using 4byte.directory API
// Acts as a RAG tool to provide human-readable signatures when ABI resolution fails
type SignatureResolver struct {
	httpClient *http.Client
	verbose    bool
}

// FourByteSignature represents a signature from 4byte.directory API
type FourByteSignature struct {
	ID             int    `json:"id"`
	TextSignature  string `json:"text_signature"`
	HexSignature   string `json:"hex_signature"`
	BytesSignature string `json:"bytes_signature,omitempty"`
	CreatedAt      string `json:"created_at,omitempty"`
}

// FourByteResponse represents the API response from 4byte.directory
type FourByteResponse struct {
	Count    int                 `json:"count"`
	Next     *string             `json:"next"`
	Previous *string             `json:"previous"`
	Results  []FourByteSignature `json:"results"`
}

// ResolvedSignature contains resolved signature information
type ResolvedSignature struct {
	Signature     string `json:"signature"`      // Raw signature hash
	TextSignature string `json:"text_signature"` // Human-readable signature
	Type          string `json:"type"`           // "function" or "event"
	Source        string `json:"source"`         // "abi" or "4byte"
}

// NewSignatureResolver creates a new signature resolver
func NewSignatureResolver() *SignatureResolver {
	return &SignatureResolver{
		httpClient: &http.Client{
			Timeout: 300 * time.Second, // 5 minutes for signature lookups
		},
		verbose: false,
	}
}

// SetVerbose enables or disables verbose logging
func (s *SignatureResolver) SetVerbose(verbose bool) {
	s.verbose = verbose
}

// Name returns the tool name
func (s *SignatureResolver) Name() string {
	return "signature_resolver"
}

// Description returns the tool description
func (s *SignatureResolver) Description() string {
	return "Resolves function and event signatures using 4byte.directory when ABI resolution fails"
}

// Dependencies returns the tools this processor depends on
func (s *SignatureResolver) Dependencies() []string {
	return []string{"abi_resolver", "log_decoder", "trace_decoder"}
}

// Process resolves unknown signatures and adds human-readable names to context
func (s *SignatureResolver) Process(ctx context.Context, baggage map[string]interface{}) error {
	if s.verbose || os.Getenv("DEBUG") == "true" {
		fmt.Printf("=== SIGNATURE RESOLVER DEBUG ===\n")
	}

	resolvedSignatures := make(map[string]*ResolvedSignature)

	// Collect unknown event signatures from log decoder
	if err := s.resolveEventSignatures(ctx, baggage, resolvedSignatures); err != nil {
		if s.verbose || os.Getenv("DEBUG") == "true" {
			fmt.Printf("Event signature resolution failed: %v\n", err)
		}
		// Don't fail the pipeline, just log the error
	}

	// Collect unknown function signatures from trace decoder
	if err := s.resolveFunctionSignatures(ctx, baggage, resolvedSignatures); err != nil {
		if s.verbose || os.Getenv("DEBUG") == "true" {
			fmt.Printf("Function signature resolution failed: %v\n", err)
		}
		// Don't fail the pipeline, just log the error
	}

	// Add resolved signatures to baggage for LLM context
	baggage["resolved_signatures"] = resolvedSignatures

	if s.verbose || os.Getenv("DEBUG") == "true" {
		fmt.Printf("Resolved %d signatures from 4byte.directory\n", len(resolvedSignatures))
		for hash, sig := range resolvedSignatures {
			fmt.Printf("  %s -> %s (%s)\n", hash, sig.TextSignature, sig.Type)
		}
		fmt.Printf("=== END SIGNATURE RESOLVER DEBUG ===\n\n")
	}

	return nil
}

// resolveEventSignatures resolves unknown event signatures
func (s *SignatureResolver) resolveEventSignatures(ctx context.Context, baggage map[string]interface{}, resolved map[string]*ResolvedSignature) error {
	events, ok := baggage["events"].([]models.Event)
	if !ok {
		return nil // No events to process
	}

	// Get resolved contracts to check what we already know
	resolvedContracts, _ := baggage["resolved_contracts"].(map[string]*ContractInfo)

	unknownSignatures := make(map[string]bool)

	for _, event := range events {
		// Skip if we already have a human-readable name from ABI
		if event.Name != "" && !strings.HasPrefix(event.Name, "0x") {
			continue
		}

		// Check if this event's signature was resolved via ABI
		hasABIResolution := false
		if resolvedContracts != nil {
			if contractInfo, exists := resolvedContracts[strings.ToLower(event.Contract)]; exists && contractInfo.IsVerified {
				for _, method := range contractInfo.ParsedABI {
					if method.Type == "event" && len(event.Topics) > 0 && method.Hash == event.Topics[0] {
						hasABIResolution = true
						break
					}
				}
			}
		}

		// Only look up signatures that don't have ABI resolution
		if !hasABIResolution && len(event.Topics) > 0 {
			signature := event.Topics[0] // First topic is event signature
			if signature != "" && strings.HasPrefix(signature, "0x") {
				unknownSignatures[signature] = true
			}
		}
	}

	if s.verbose || os.Getenv("DEBUG") == "true" {
		fmt.Printf("Found %d unknown event signatures to resolve\n", len(unknownSignatures))
	}

	// Resolve unknown event signatures
	for signature := range unknownSignatures {
		if resolvedSig, err := s.lookupEventSignature(ctx, signature); err == nil {
			resolved[signature] = resolvedSig
		}
		// Small delay to be respectful to the API
		time.Sleep(100 * time.Millisecond)
	}

	return nil
}

// resolveFunctionSignatures resolves unknown function signatures from traces
func (s *SignatureResolver) resolveFunctionSignatures(ctx context.Context, baggage map[string]interface{}, resolved map[string]*ResolvedSignature) error {
	calls, ok := baggage["calls"].([]models.Call)
	if !ok {
		return nil // No calls to process
	}

	// Get resolved contracts to check what we already know
	resolvedContracts, _ := baggage["resolved_contracts"].(map[string]*ContractInfo)

	unknownSignatures := make(map[string]bool)

	for _, call := range calls {
		// Skip if we already have a human-readable method name from ABI
		if call.Method != "" && !strings.HasPrefix(call.Method, "0x") {
			continue
		}

		// Check if this call's signature was resolved via ABI
		hasABIResolution := false
		if resolvedContracts != nil {
			if contractInfo, exists := resolvedContracts[strings.ToLower(call.Contract)]; exists && contractInfo.IsVerified {
				// For function calls, we'd need the 4-byte selector from trace data
				// This would require trace decoder enhancement to capture selectors
				hasABIResolution = false // For now, assume we need to look up all function signatures
			}
		}

		// Extract function selector from call data if available
		// This would need to be provided by trace decoder
		if callData, ok := call.Arguments["input"].(string); ok && len(callData) >= 10 {
			selector := callData[:10] // First 4 bytes (8 hex chars + 0x)
			if !hasABIResolution && strings.HasPrefix(selector, "0x") {
				unknownSignatures[selector] = true
			}
		}
	}

	if s.verbose || os.Getenv("DEBUG") == "true" {
		fmt.Printf("Found %d unknown function signatures to resolve\n", len(unknownSignatures))
	}

	// Resolve unknown function signatures
	for signature := range unknownSignatures {
		if resolvedSig, err := s.lookupFunctionSignature(ctx, signature); err == nil {
			resolved[signature] = resolvedSig
		}
		// Small delay to be respectful to the API
		time.Sleep(100 * time.Millisecond)
	}

	return nil
}

// lookupEventSignature looks up an event signature on 4byte.directory
func (s *SignatureResolver) lookupEventSignature(ctx context.Context, hexSignature string) (*ResolvedSignature, error) {
	baseURL := "https://www.4byte.directory/api/v1/event-signatures/"

	params := url.Values{}
	params.Set("hex_signature", hexSignature)

	apiURL := baseURL + "?" + params.Encode()

	if s.verbose || os.Getenv("DEBUG") == "true" {
		fmt.Printf("  Looking up event signature: %s\n", hexSignature)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var response FourByteResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(response.Results) == 0 {
		return nil, fmt.Errorf("no signatures found")
	}

	// Return the first (most common) signature
	result := response.Results[0]

	if s.verbose || os.Getenv("DEBUG") == "true" {
		fmt.Printf("  ✅ Found event: %s\n", result.TextSignature)
	}

	return &ResolvedSignature{
		Signature:     hexSignature,
		TextSignature: result.TextSignature,
		Type:          "event",
		Source:        "4byte",
	}, nil
}

// lookupFunctionSignature looks up a function signature on 4byte.directory
func (s *SignatureResolver) lookupFunctionSignature(ctx context.Context, hexSignature string) (*ResolvedSignature, error) {
	baseURL := "https://www.4byte.directory/api/v1/signatures/"

	params := url.Values{}
	params.Set("hex_signature", hexSignature)

	apiURL := baseURL + "?" + params.Encode()

	if s.verbose || os.Getenv("DEBUG") == "true" {
		fmt.Printf("  Looking up function signature: %s\n", hexSignature)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var response FourByteResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(response.Results) == 0 {
		return nil, fmt.Errorf("no signatures found")
	}

	// Return the first (most common) signature
	result := response.Results[0]

	if s.verbose || os.Getenv("DEBUG") == "true" {
		fmt.Printf("  ✅ Found function: %s\n", result.TextSignature)
	}

	return &ResolvedSignature{
		Signature:     hexSignature,
		TextSignature: result.TextSignature,
		Type:          "function",
		Source:        "4byte",
	}, nil
}

// GetPromptContext provides context about resolved signatures for LLM
func (s *SignatureResolver) GetPromptContext(ctx context.Context, baggage map[string]interface{}) string {
	resolvedSignatures, ok := baggage["resolved_signatures"].(map[string]*ResolvedSignature)
	if !ok || len(resolvedSignatures) == 0 {
		return ""
	}

	var contextParts []string
	contextParts = append(contextParts, "### 4byte.directory Signature Resolutions:")

	var eventSigs, functionSigs []string

	for _, sig := range resolvedSignatures {
		if sig.Type == "event" {
			eventSigs = append(eventSigs, fmt.Sprintf("- %s: %s", sig.Signature, sig.TextSignature))
		} else if sig.Type == "function" {
			functionSigs = append(functionSigs, fmt.Sprintf("- %s: %s", sig.Signature, sig.TextSignature))
		}
	}

	if len(eventSigs) > 0 {
		contextParts = append(contextParts, "")
		contextParts = append(contextParts, "Event Signatures:")
		contextParts = append(contextParts, strings.Join(eventSigs, "\n"))
	}

	if len(functionSigs) > 0 {
		contextParts = append(contextParts, "")
		contextParts = append(contextParts, "Function Signatures:")
		contextParts = append(contextParts, strings.Join(functionSigs, "\n"))
	}

	contextParts = append(contextParts, "")
	contextParts = append(contextParts, "Use these human-readable signatures when analyzing transaction calls and events that don't have ABI resolution.")

	return strings.Join(contextParts, "\n")
}

// GetRagContext provides RAG context for function calling (if needed)
func (s *SignatureResolver) GetRagContext(ctx context.Context, baggage map[string]interface{}) *RagContext {
	// This tool primarily provides context rather than search functionality
	// But could be extended to allow LLM to search for specific signatures
	return NewRagContext()
}
