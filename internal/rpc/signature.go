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
			Timeout: 5 * time.Second,
		},
		rpcClient:   rpcClient,
		cache:       make(map[string]*SignatureInfo),
		use4ByteAPI: use4ByteAPI,
	}
}

// Common function signatures (most reliable - from RPC experience)
var commonFunctionSignatures = map[string]string{
	// ERC20
	"0xa9059cbb": "transfer(address,uint256)",
	"0x23b872dd": "transferFrom(address,address,uint256)",
	"0x095ea7b3": "approve(address,uint256)",
	"0x70a08231": "balanceOf(address)",
	"0xdd62ed3e": "allowance(address,address)",
	"0x18160ddd": "totalSupply()",
	"0x06fdde03": "name()",
	"0x95d89b41": "symbol()",
	"0x313ce567": "decimals()",

	// ERC721
	"0x42842e0e": "safeTransferFrom(address,address,uint256)",
	"0xb88d4fde": "safeTransferFrom(address,address,uint256,bytes)",
	"0x6352211e": "ownerOf(uint256)",
	"0xc87b56dd": "tokenURI(uint256)",
	"0xa22cb465": "setApprovalForAll(address,bool)",
	"0x081812fc": "getApproved(uint256)",
	"0xe985e9c5": "isApprovedForAll(address,address)",

	// Common DeFi
	"0x7ff36ab5": "swapExactETHForTokens(uint256,address[],address,uint256)",
	"0x38ed1739": "swapExactTokensForTokens(uint256,uint256,address[],address,uint256)",
	"0xfb3bdb41": "swapETHForExactTokens(uint256,address[],address,uint256)",
	"0x8803dbee": "swapTokensForExactTokens(uint256,uint256,address[],address,uint256)",
	"0xe8e33700": "addLiquidity(address,address,uint256,uint256,uint256,uint256,address,uint256)",
	"0xf305d719": "addLiquidityETH(address,uint256,uint256,uint256,address,uint256)",
	"0x02751cec": "removeLiquidity(address,address,uint256,uint256,uint256,address,uint256)",
	"0xaf2979eb": "removeLiquidityETH(address,uint256,uint256,uint256,address,uint256)",

	// Staking/Rewards
	"0xa694fc3a": "stake(uint256)",
	"0x2e17de78": "unstake(uint256)",
	"0x3d18b912": "getReward()",
	"0xe9fad8ee": "exit()",

	// Governance
	"0x15373e3d": "propose(address[],uint256[],string[],bytes[],string)",
	"0x56781388": "castVote(uint256,uint8)",
	"0x7b3c71d3": "delegate(address)",
}

// Common event signatures
var commonEventSignatures = map[string]string{
	// ERC20 Events
	"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef": "Transfer(address,address,uint256)",
	"0x8c5be1e5ebec7d5bd14f71427d1e84f3dd0314c0f7b2291e5b200ac8c7c3b925": "Approval(address,address,uint256)",

	// ERC721 Events
	"0x17307eab39ab6107e8899845ad3d59bd9653f200f220920489ca2b5937696c31": "ApprovalForAll(address,address,bool)",

	// Uniswap V2 Events
	"0xd78ad95fa46c994b6551d0da85fc275fe613ce37657fb8d5e3d130840159d822": "Swap(address,uint256,uint256,uint256,uint256,address)",
	"0x1c411e9a96e071241c2f21f7726b17ae89e3cab4c78be50e062b03a9fffbbad1": "Sync(uint112,uint112)",
	"0x4c209b5fc8ad50758f13e2e1088ba56a560dff690a1c6fef26394f4c03821c4f": "Mint(address,uint256,uint256)",
	"0xcc16f5dbb4873280815c1ee09dbd06736cffcc184412cf7a71a0fdb75d397ca5": "Burn(address,uint256,uint256,address)",

	// Uniswap V2 Factory
	"0x0d3648bd0f6ba80134a33ba9275ac585d9d315f0ad8355cddefde31afa28d0e9": "PairCreated(address,address,address,uint256)",

	// Common DeFi events
	"0x2f00e3cdd69a77be7ed215ec7b2a36784dd158f921fca79ac29ffa6880a6be49": "Deposit(address,uint256)",
	"0x884edad9ce6fa2440d8a54cc123490eb96d2768479d49ff9c7366125a9424364": "Withdraw(address,uint256)",
	"0x90890809c654f11d6e72a28fa60149770a0d11ec6c92319d6ceb2bb0a4ea1a15": "Staked(address,uint256)",
	"0x0f5bb82176feb1b5e747e28471aa92156a04d9f3ab9f45f28e2d704232b93f75": "Unstaked(address,uint256)",
}

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

	// Check common signatures first (most reliable)
	if textSig, exists := commonFunctionSignatures[sig]; exists {
		info := &SignatureInfo{
			Signature: textSig,
			Name:      extractFunctionName(textSig),
			Type:      "function",
		}
		sr.cache[sig] = info
		return info, nil
	}

	// If 4byte API is enabled, try it
	if sr.use4ByteAPI {
		if info, err := sr.resolve4Byte(ctx, sig); err == nil && info != nil {
			sr.cache[sig] = info
			return info, nil
		}
	}

	// Return unknown signature
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

	// Check common event signatures first
	if textSig, exists := commonEventSignatures[sig]; exists {
		info := &SignatureInfo{
			Signature: textSig,
			Name:      extractEventName(textSig),
			Type:      "event",
		}
		sr.cache[sig] = info
		return info, nil
	}

	// If 4byte API is enabled, try event signatures API
	if sr.use4ByteAPI {
		if info, err := sr.resolveEventSignature4Byte(ctx, sig); err == nil && info != nil {
			sr.cache[sig] = info
			return info, nil
		}
	}

	// Return unknown event signature
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
