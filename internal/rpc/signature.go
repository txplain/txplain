package rpc

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/sha3"
)

// SignatureInfo represents information about a function or event signature
type SignatureInfo struct {
	Signature string `json:"signature"`
	Name      string `json:"name"`
	Type      string `json:"type"` // function, event
}

// FourByteResponse represents the response from 4byte.directory API
type FourByteResponse struct {
	Count   int `json:"count"`
	Results []struct {
		ID        int    `json:"id"`
		Signature string `json:"text_signature"`
		Hex       string `json:"hex_signature"`
	} `json:"results"`
}

// SignatureResolver handles function and event signature resolution
type SignatureResolver struct {
	client      *http.Client
	rpcClient   *Client
	cache       map[string]*SignatureInfo
	use4ByteAPI bool
}

// NewSignatureResolver creates a new signature resolver
func NewSignatureResolver(rpcClient *Client, use4ByteAPI bool) *SignatureResolver {
	return &SignatureResolver{
		client: &http.Client{
			Timeout: 300 * time.Second, // 5 minutes for signature resolution
		},
		rpcClient:   rpcClient,
		cache:       make(map[string]*SignatureInfo),
		use4ByteAPI: use4ByteAPI,
	}
}

// Signature resolution should rely on:
// 1. ABI data from verified contracts (primary source)
// 2. 4byte.directory API (secondary source)
// 3. Generic fallback (no hardcoded assumptions)

// ResolveFunctionSignature resolves a function signature to its human-readable form
func (sr *SignatureResolver) ResolveFunctionSignature(ctx context.Context, signature string) (*SignatureInfo, error) {
	// Normalize signature
	sig := strings.ToLower(signature)
	if !strings.HasPrefix(sig, "0x") {
		sig = "0x" + sig
	}

	// Check cache first
	if cached, exists := sr.cache[sig]; exists {
		return cached, nil
	}

	// Try 4byte API if enabled (generic approach - no hardcoded assumptions)
	if sr.use4ByteAPI {
		if info, err := sr.resolve4Byte(ctx, sig); err == nil && info != nil {
			sr.cache[sig] = info
			return info, nil
		}
	}

	// Return generic information - let ABI resolver and LLM interpret
	info := &SignatureInfo{
		Signature: signature,
		Name:      "unknown",
		Type:      "function",
	}
	sr.cache[sig] = info
	return info, nil
}

// ResolveEventSignature resolves an event signature to its human-readable form
func (sr *SignatureResolver) ResolveEventSignature(ctx context.Context, signature string) (*SignatureInfo, error) {
	// Normalize signature
	sig := strings.ToLower(signature)
	if !strings.HasPrefix(sig, "0x") {
		sig = "0x" + sig
	}

	// Check cache first
	if cached, exists := sr.cache[sig]; exists {
		return cached, nil
	}

	// Try 4byte API if enabled for event signatures (generic approach)
	if sr.use4ByteAPI {
		if info, err := sr.resolveEventSignature4Byte(ctx, sig); err == nil && info != nil {
			sr.cache[sig] = info
			return info, nil
		}
	}

	// Return generic information - let ABI resolver and LLM interpret
	info := &SignatureInfo{
		Signature: signature,
		Name:      "unknown",
		Type:      "event",
	}
	sr.cache[sig] = info
	return info, nil
}

// resolveEventSignature4Byte queries the 4byte.directory API for event signatures
func (sr *SignatureResolver) resolveEventSignature4Byte(ctx context.Context, signature string) (*SignatureInfo, error) {
	url := fmt.Sprintf("https://www.4byte.directory/api/v1/event-signatures/?hex_signature=%s", signature)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := sr.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("4byte event API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var fourByteResp FourByteResponse
	if err := json.Unmarshal(body, &fourByteResp); err != nil {
		return nil, err
	}

	if len(fourByteResp.Results) == 0 {
		return nil, fmt.Errorf("no event signature results found")
	}

	// Use the first result
	result := fourByteResp.Results[0]
	return &SignatureInfo{
		Signature: result.Signature,
		Name:      extractEventName(result.Signature),
		Type:      "event",
	}, nil
}

// resolve4Byte queries the 4byte.directory API for function signatures
func (sr *SignatureResolver) resolve4Byte(ctx context.Context, signature string) (*SignatureInfo, error) {
	url := fmt.Sprintf("https://www.4byte.directory/api/v1/signatures/?hex_signature=%s", signature)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := sr.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("4byte API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var fourByteResp FourByteResponse
	if err := json.Unmarshal(body, &fourByteResp); err != nil {
		return nil, err
	}

	if len(fourByteResp.Results) == 0 {
		return nil, fmt.Errorf("no results found")
	}

	// Use the first result
	result := fourByteResp.Results[0]
	return &SignatureInfo{
		Signature: result.Signature,
		Name:      extractFunctionName(result.Signature),
		Type:      "function",
	}, nil
}

// GenerateSignature generates a method signature hash from the text signature
func GenerateSignature(textSignature string) string {
	hash := sha3.NewLegacyKeccak256()
	hash.Write([]byte(textSignature))
	hashBytes := hash.Sum(nil)
	return "0x" + hex.EncodeToString(hashBytes[:4])
}

// GenerateEventSignature generates an event signature hash from the text signature
func GenerateEventSignature(textSignature string) string {
	hash := sha3.NewLegacyKeccak256()
	hash.Write([]byte(textSignature))
	hashBytes := hash.Sum(nil)
	return "0x" + hex.EncodeToString(hashBytes)
}

// extractFunctionName extracts the function name from a text signature
func extractFunctionName(textSignature string) string {
	if idx := strings.Index(textSignature, "("); idx > 0 {
		return textSignature[:idx]
	}
	return textSignature
}

// extractEventName extracts the event name from a text signature
func extractEventName(textSignature string) string {
	if idx := strings.Index(textSignature, "("); idx > 0 {
		return textSignature[:idx]
	}
	return textSignature
}

// ValidateSignature checks if a signature matches the expected pattern
func ValidateSignature(signature string) bool {
	if !strings.HasPrefix(signature, "0x") {
		return false
	}
	if len(signature) != 10 { // 0x + 8 hex chars = 10 total
		return false
	}

	// Check if all characters after 0x are valid hex
	for _, r := range signature[2:] {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// GetSignatureStats returns statistics about cached signatures
func (sr *SignatureResolver) GetSignatureStats() map[string]int {
	stats := make(map[string]int)

	for _, info := range sr.cache {
		stats[info.Type]++
	}

	stats["total"] = len(sr.cache)
	return stats
}
