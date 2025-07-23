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
	"golang.org/x/crypto/sha3"
)

// ABIResolver fetches contract ABIs and source code from Etherscan API v2 and Sourcify
type ABIResolver struct {
	httpClient *http.Client
	apiKey     string
}

// ContractInfo represents resolved contract information
type ContractInfo struct {
	Address          string      `json:"address"`
	ABI              string      `json:"abi"`           // Raw ABI JSON string
	SourceCode       string      `json:"source_code"`   // Contract source code
	ContractName     string      `json:"contract_name"` // Name from verification
	CompilerVersion  string      `json:"compiler_version"`
	IsVerified       bool        `json:"is_verified"`
	IsProxy          bool        `json:"is_proxy"`
	Implementation   string      `json:"implementation,omitempty"`    // For proxy contracts
	IsImplementation bool        `json:"is_implementation,omitempty"` // True if this is an implementation contract
	ProxyAddress     string      `json:"proxy_address,omitempty"`     // Address of the proxy that uses this implementation
	ParsedABI        []ABIMethod `json:"parsed_abi"`                  // Parsed ABI for easier access
}

// ABIMethod represents a parsed ABI method or event
type ABIMethod struct {
	Name      string     `json:"name"`
	Type      string     `json:"type"`      // function, event, constructor, etc.
	Signature string     `json:"signature"` // e.g. "Transfer(address,address,uint256)"
	Hash      string     `json:"hash"`      // 4-byte hash for functions, topic hash for events
	Inputs    []ABIInput `json:"inputs"`
}

// ABIInput represents an ABI input parameter
type ABIInput struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	Indexed      bool   `json:"indexed,omitempty"`      // For events
	InternalType string `json:"internalType,omitempty"` // For structs
}

// EtherscanResponse represents the API response structure
type EtherscanResponse struct {
	Status  string      `json:"status"`
	Message string      `json:"message"`
	Result  interface{} `json:"result"`
}

// SourceCodeResult represents the structure for source code API responses
type SourceCodeResult struct {
	SourceCode           string `json:"SourceCode"`
	ABI                  string `json:"ABI"`
	ContractName         string `json:"ContractName"`
	CompilerVersion      string `json:"CompilerVersion"`
	OptimizationUsed     string `json:"OptimizationUsed"`
	Runs                 string `json:"Runs"`
	ConstructorArguments string `json:"ConstructorArguments"`
	EVMVersion           string `json:"EVMVersion"`
	Library              string `json:"Library"`
	LicenseType          string `json:"LicenseType"`
	Proxy                string `json:"Proxy"`
	Implementation       string `json:"Implementation"`
	SwarmSource          string `json:"SwarmSource"`
}

// NewABIResolver creates a new ABI resolver with Etherscan API key
func NewABIResolver() *ABIResolver {
	apiKey := os.Getenv("ETHERSCAN_API_KEY")
	if apiKey == "" {
		fmt.Println("Warning: ETHERSCAN_API_KEY not set, ABI resolution will be limited")
	}

	return &ABIResolver{
		httpClient: &http.Client{
			Timeout: 60 * time.Second, // Increased for slow Etherscan responses
		},
		apiKey: apiKey,
	}
}

// Name returns the tool name
func (a *ABIResolver) Name() string {
	return "abi_resolver"
}

// Description returns the tool description
func (a *ABIResolver) Description() string {
	return "Resolves contract ABIs and source code from Etherscan API v2 and Sourcify for verified contracts"
}

// Dependencies returns the tools this processor depends on (none - runs first)
func (a *ABIResolver) Dependencies() []string {
	return []string{} // No dependencies, runs first in pipeline
}

// Process resolves ABIs for all contract addresses and adds to baggage
func (a *ABIResolver) Process(ctx context.Context, baggage map[string]interface{}) error {
	// Extract all contract addresses from transaction data
	contractAddresses := a.extractContractAddresses(baggage)

	if len(contractAddresses) == 0 {
		// Add empty resolved contracts to baggage
		baggage["resolved_contracts"] = make(map[string]*ContractInfo)
		return nil
	}

	// Get network ID for appropriate Etherscan API endpoint
	networkID := int64(1) // Default to Ethereum mainnet
	if rawData, ok := baggage["raw_data"].(map[string]interface{}); ok {
		if nid, ok := rawData["network_id"].(float64); ok {
			networkID = int64(nid)
		}
	}

	// Resolve contracts
	resolvedContracts := make(map[string]*ContractInfo)
	for _, address := range contractAddresses {
		if contractInfo, err := a.resolveContract(ctx, address, networkID); err == nil {
			resolvedContracts[strings.ToLower(address)] = contractInfo

			// If this is a proxy contract, also resolve the implementation contract
			if contractInfo.IsProxy && contractInfo.Implementation != "" {
				// Add small delay before resolving implementation
				time.Sleep(200 * time.Millisecond)

				if implInfo, err := a.resolveContract(ctx, contractInfo.Implementation, networkID); err == nil {
					// Store implementation contract info with a clear key
					implKey := strings.ToLower(contractInfo.Implementation)
					resolvedContracts[implKey] = implInfo

					// Mark this as an implementation contract for context
					implInfo.IsImplementation = true
					implInfo.ProxyAddress = address
				}
			}
		}

		// Add small delay to respect API rate limits
		time.Sleep(200 * time.Millisecond)
	}

	// Add resolved contracts to baggage
	baggage["resolved_contracts"] = resolvedContracts

	// Also add the list of all discovered contract addresses for other tools to use
	baggage["contract_addresses"] = contractAddresses
	return nil
}

// extractContractAddresses extracts all unique contract addresses from transaction data
func (a *ABIResolver) extractContractAddresses(baggage map[string]interface{}) []string {
	addressMap := make(map[string]bool)
	var addresses []string

	// From raw transaction data
	if rawDataInterface, ok := baggage["raw_data"]; ok {
		if rawData, ok := rawDataInterface.(map[string]interface{}); ok {
			// Transaction 'to' address (main contract being called)
			if receipt, ok := rawData["receipt"].(map[string]interface{}); ok {
				if to, ok := receipt["to"].(string); ok && to != "" && to != "0x" {
					addressMap[strings.ToLower(to)] = true
				}
			}

			// From ALL logs - this is the comprehensive approach
			// Every log entry has an 'address' field which is the contract that emitted it
			if logs, ok := rawData["logs"].([]interface{}); ok {
				for _, logInterface := range logs {
					if logMap, ok := logInterface.(map[string]interface{}); ok {
						if address, ok := logMap["address"].(string); ok && address != "" {
							addressMap[strings.ToLower(address)] = true
						}
					}
				}
			}

			// Also extract from receipt logs (backup in case rawData logs are missing)
			if receipt, ok := rawData["receipt"].(map[string]interface{}); ok {
				if logs, ok := receipt["logs"].([]interface{}); ok {
					for _, logInterface := range logs {
						if logMap, ok := logInterface.(map[string]interface{}); ok {
							if address, ok := logMap["address"].(string); ok && address != "" {
								addressMap[strings.ToLower(address)] = true
							}
						}
					}
				}
			}

			// From trace data (if available) - get all contracts called
			if trace, ok := rawData["trace"].(map[string]interface{}); ok {
				// Extract addresses from trace calls
				if traceResult, ok := trace["result"].(map[string]interface{}); ok {
					if calls, ok := traceResult["calls"].([]interface{}); ok {
						for _, call := range calls {
							if callMap, ok := call.(map[string]interface{}); ok {
								if to, ok := callMap["to"].(string); ok && to != "" {
									addressMap[strings.ToLower(to)] = true
								}
							}
						}
					}
				}

				// Also get the main trace 'to' address
				if to, ok := trace["to"].(string); ok && to != "" {
					addressMap[strings.ToLower(to)] = true
				}
			}
		}
	}

	// GENERIC: Extract ALL address parameters from ALL events
	// This works with any event type without hardcoded event names
	if events, ok := baggage["events"].([]models.Event); ok {
		for _, event := range events {
			if event.Parameters != nil {
				// Extract ALL address-like parameters from ANY event
				for _, paramValue := range event.Parameters {
					if addressStr, ok := paramValue.(string); ok && addressStr != "" {
						// Check if this looks like an address (42 chars starting with 0x, or 66 chars padded)
						if a.looksLikeAddress(addressStr) {
							cleanAddress := a.cleanAddress(addressStr)
							if cleanAddress != "" && cleanAddress != "0x" {
								addressMap[strings.ToLower(cleanAddress)] = true
							}
						}
					}
				}
			}
		}
	}

	// Also extract from decoded events (if any) - this catches edge cases
	if events, ok := baggage["events"].([]models.Event); ok {
		for _, event := range events {
			if event.Contract != "" {
				addressMap[strings.ToLower(event.Contract)] = true
			}
		}
	}

	// Convert map to slice
	for address := range addressMap {
		addresses = append(addresses, address)
	}

	return addresses
}

// resolveContract fetches contract information from Etherscan API with Sourcify fallback
func (a *ABIResolver) resolveContract(ctx context.Context, address string, networkID int64) (*ContractInfo, error) {
	contractInfo := &ContractInfo{
		Address:    address,
		IsVerified: false,
	}

	// First, try Etherscan if API key is available
	if a.apiKey != "" {
		// Get the appropriate Etherscan API endpoint
		baseURL := a.getEtherscanURL(networkID)
		if baseURL != "" {
			// Try to get contract source code from Etherscan
			if err := a.fetchSourceCode(ctx, baseURL, address, contractInfo); err == nil {
				// Success - parse ABI and return
				if contractInfo.ABI != "" {
					if parsedABI, err := a.parseABI(contractInfo.ABI); err == nil {
						contractInfo.ParsedABI = parsedABI
					}
				}
				return contractInfo, nil
			}

			// If source code fetch fails, try ABI only from Etherscan
			if err := a.fetchABI(ctx, baseURL, address, contractInfo); err == nil {
				// Success - parse ABI and return
				if contractInfo.ABI != "" {
					if parsedABI, err := a.parseABI(contractInfo.ABI); err == nil {
						contractInfo.ParsedABI = parsedABI
					}
				}
				return contractInfo, nil
			}
		}
	}

	// Etherscan failed or no API key, try Sourcify as fallback
	if err := a.fetchFromSourceify(ctx, address, networkID, contractInfo); err != nil {
		return nil, fmt.Errorf("failed to resolve contract from Etherscan and Sourcify: %w", err)
	}

	// Parse ABI if we have it
	if contractInfo.ABI != "" {
		if parsedABI, err := a.parseABI(contractInfo.ABI); err == nil {
			contractInfo.ParsedABI = parsedABI
		}
	}

	return contractInfo, nil
}

// fetchFromSourceify fetches contract information from Sourcify
func (a *ABIResolver) fetchFromSourceify(ctx context.Context, address string, networkID int64, contractInfo *ContractInfo) error {
	// Sourcify uses standard chain IDs - convert our network ID if needed
	chainID := networkID

	// Check if contract is verified on Sourcify first
	serverURL := "https://sourcify.dev/server"
	checkURL := fmt.Sprintf("%s/check-by-addresses?addresses=%s&chainIds=%d", serverURL, address, chainID)

	req, err := http.NewRequestWithContext(ctx, "GET", checkURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create Sourcify check request: %w", err)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to check Sourcify: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Sourcify check failed with status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read Sourcify check response: %w", err)
	}

	// Parse the response to check if contract is verified
	var checkResult []map[string]interface{}
	if err := json.Unmarshal(body, &checkResult); err != nil {
		return fmt.Errorf("failed to parse Sourcify check response: %w", err)
	}

	// Check if we got any results
	if len(checkResult) == 0 {
		return fmt.Errorf("contract not found on Sourcify")
	}

	// Look for full match or partial match
	result := checkResult[0]
	status, ok := result["status"].(string)
	if !ok || (status != "perfect" && status != "partial") {
		return fmt.Errorf("contract not verified on Sourcify (status: %v)", status)
	}

	// Contract is verified, fetch the metadata.json file
	repoURL := "https://repo.sourcify.dev"
	var metadataURL string

	if status == "perfect" {
		metadataURL = fmt.Sprintf("%s/contracts/full_match/%d/%s/metadata.json", repoURL, chainID, address)
	} else {
		metadataURL = fmt.Sprintf("%s/contracts/partial_match/%d/%s/metadata.json", repoURL, chainID, address)
	}

	// Fetch metadata.json
	metadataReq, err := http.NewRequestWithContext(ctx, "GET", metadataURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create metadata request: %w", err)
	}

	metadataResp, err := a.httpClient.Do(metadataReq)
	if err != nil {
		return fmt.Errorf("failed to fetch metadata from Sourcify: %w", err)
	}
	defer metadataResp.Body.Close()

	if metadataResp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to fetch metadata from Sourcify with status %d", metadataResp.StatusCode)
	}

	metadataBody, err := io.ReadAll(metadataResp.Body)
	if err != nil {
		return fmt.Errorf("failed to read metadata response: %w", err)
	}

	// Parse metadata to extract ABI
	var metadata map[string]interface{}
	if err := json.Unmarshal(metadataBody, &metadata); err != nil {
		return fmt.Errorf("failed to parse metadata JSON: %w", err)
	}

	// Extract ABI from output.abi field
	if output, ok := metadata["output"].(map[string]interface{}); ok {
		if abi, ok := output["abi"].([]interface{}); ok {
			// Convert ABI back to JSON string
			abiBytes, err := json.Marshal(abi)
			if err != nil {
				return fmt.Errorf("failed to marshal ABI: %w", err)
			}
			contractInfo.ABI = string(abiBytes)
			contractInfo.IsVerified = true
		}
	}

	// Extract contract name if available
	if settings, ok := metadata["settings"].(map[string]interface{}); ok {
		if compilationTarget, ok := settings["compilationTarget"].(map[string]interface{}); ok {
			// Get the first contract name from compilation target
			for _, name := range compilationTarget {
				if nameStr, ok := name.(string); ok {
					contractInfo.ContractName = nameStr
					break
				}
			}
		}
	}

	// Extract compiler version if available
	if compiler, ok := metadata["compiler"].(map[string]interface{}); ok {
		if version, ok := compiler["version"].(string); ok {
			contractInfo.CompilerVersion = version
		}
	}

	if contractInfo.ABI == "" {
		return fmt.Errorf("no ABI found in Sourcify metadata")
	}

	return nil
}

// fetchSourceCode fetches contract source code and metadata
func (a *ABIResolver) fetchSourceCode(ctx context.Context, baseURL, address string, contractInfo *ContractInfo) error {
	// Build URL for getsourcecode API
	params := url.Values{}
	params.Set("module", "contract")
	params.Set("action", "getsourcecode")
	params.Set("address", address)
	params.Set("apikey", a.apiKey)

	apiURL := baseURL + "/api?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var etherscanResp EtherscanResponse
	if err := json.Unmarshal(body, &etherscanResp); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if etherscanResp.Status != "1" {
		return fmt.Errorf("API error: %s", etherscanResp.Message)
	}

	// Parse result array
	resultArray, ok := etherscanResp.Result.([]interface{})
	if !ok || len(resultArray) == 0 {
		return fmt.Errorf("unexpected result format")
	}

	// Convert to SourceCodeResult
	resultBytes, err := json.Marshal(resultArray[0])
	if err != nil {
		return fmt.Errorf("failed to marshal result: %w", err)
	}

	var sourceResult SourceCodeResult
	if err := json.Unmarshal(resultBytes, &sourceResult); err != nil {
		return fmt.Errorf("failed to unmarshal source result: %w", err)
	}

	// Check if contract is verified
	if sourceResult.SourceCode == "" && sourceResult.ABI == "Contract source code not verified" {
		return fmt.Errorf("contract not verified")
	}

	// Fill in contract info
	contractInfo.SourceCode = sourceResult.SourceCode
	contractInfo.ABI = sourceResult.ABI
	contractInfo.ContractName = sourceResult.ContractName
	contractInfo.CompilerVersion = sourceResult.CompilerVersion
	contractInfo.IsVerified = true

	// Check if it's a proxy
	if sourceResult.Proxy == "1" && sourceResult.Implementation != "" {
		contractInfo.IsProxy = true
		contractInfo.Implementation = sourceResult.Implementation
	}

	return nil
}

// fetchABI fetches only the contract ABI (fallback)
func (a *ABIResolver) fetchABI(ctx context.Context, baseURL, address string, contractInfo *ContractInfo) error {
	// Build URL for getabi API
	params := url.Values{}
	params.Set("module", "contract")
	params.Set("action", "getabi")
	params.Set("address", address)
	params.Set("apikey", a.apiKey)

	apiURL := baseURL + "/api?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var etherscanResp EtherscanResponse
	if err := json.Unmarshal(body, &etherscanResp); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if etherscanResp.Status != "1" {
		return fmt.Errorf("API error: %s", etherscanResp.Message)
	}

	abi, ok := etherscanResp.Result.(string)
	if !ok {
		return fmt.Errorf("unexpected result format")
	}

	if abi == "Contract source code not verified" {
		return fmt.Errorf("contract not verified")
	}

	contractInfo.ABI = abi
	contractInfo.IsVerified = true

	return nil
}

// parseABI parses the ABI JSON and extracts method/event information
func (a *ABIResolver) parseABI(abiJSON string) ([]ABIMethod, error) {
	var rawABI []map[string]interface{}
	if err := json.Unmarshal([]byte(abiJSON), &rawABI); err != nil {
		return nil, fmt.Errorf("failed to parse ABI: %w", err)
	}

	var methods []ABIMethod

	for _, item := range rawABI {
		itemType, ok := item["type"].(string)
		if !ok {
			continue
		}

		name, _ := item["name"].(string)
		inputs := a.parseABIInputs(item["inputs"])

		method := ABIMethod{
			Name:   name,
			Type:   itemType,
			Inputs: inputs,
		}

		// Generate signature and hash based on type
		if itemType == "function" {
			method.Signature = a.generateFunctionSignature(name, inputs)
			method.Hash = a.generateFunctionHash(method.Signature)
		} else if itemType == "event" {
			method.Signature = a.generateEventSignature(name, inputs)
			method.Hash = a.generateEventHash(method.Signature)
		}

		methods = append(methods, method)
	}

	return methods, nil
}

// parseABIInputs parses ABI inputs from raw JSON
func (a *ABIResolver) parseABIInputs(inputsRaw interface{}) []ABIInput {
	var inputs []ABIInput

	inputsArray, ok := inputsRaw.([]interface{})
	if !ok {
		return inputs
	}

	for _, inputRaw := range inputsArray {
		inputMap, ok := inputRaw.(map[string]interface{})
		if !ok {
			continue
		}

		input := ABIInput{}
		if name, ok := inputMap["name"].(string); ok {
			input.Name = name
		}
		if inputType, ok := inputMap["type"].(string); ok {
			input.Type = inputType
		}
		if indexed, ok := inputMap["indexed"].(bool); ok {
			input.Indexed = indexed
		}
		if internalType, ok := inputMap["internalType"].(string); ok {
			input.InternalType = internalType
		}

		inputs = append(inputs, input)
	}

	return inputs
}

// generateFunctionSignature generates function signature string
func (a *ABIResolver) generateFunctionSignature(name string, inputs []ABIInput) string {
	var paramTypes []string
	for _, input := range inputs {
		paramTypes = append(paramTypes, input.Type)
	}
	return fmt.Sprintf("%s(%s)", name, strings.Join(paramTypes, ","))
}

// generateEventSignature generates event signature string
func (a *ABIResolver) generateEventSignature(name string, inputs []ABIInput) string {
	var paramTypes []string
	for _, input := range inputs {
		paramTypes = append(paramTypes, input.Type)
	}
	return fmt.Sprintf("%s(%s)", name, strings.Join(paramTypes, ","))
}

// generateFunctionHash generates 4-byte function selector
func (a *ABIResolver) generateFunctionHash(signature string) string {
	hasher := sha3.NewLegacyKeccak256()
	hasher.Write([]byte(signature))
	hash := hasher.Sum(nil)
	return "0x" + fmt.Sprintf("%x", hash[:4])
}

// generateEventHash generates 32-byte event topic hash
func (a *ABIResolver) generateEventHash(signature string) string {
	hasher := sha3.NewLegacyKeccak256()
	hasher.Write([]byte(signature))
	hash := hasher.Sum(nil)
	return "0x" + fmt.Sprintf("%x", hash)
}

// getEtherscanURL returns the appropriate explorer API URL for the network
// Priority: 1) Environment variable, 2) URL pattern matching, 3) Network config derivation
func (a *ABIResolver) getEtherscanURL(networkID int64) string {
	// Priority 1: Check for environment variable configuration
	envKey := fmt.Sprintf("ETHERSCAN_ENDPOINT_CHAIN_%d", networkID)
	if endpoint := os.Getenv(envKey); endpoint != "" {
		// If the endpoint doesn't end with /api, append it for consistency
		if !strings.HasSuffix(endpoint, "/api") {
			endpoint += "/api"
		}
		return endpoint
	}

	// Priority 2: Get network configuration for fallback
	network, exists := models.GetNetwork(networkID)
	if !exists {
		return ""
	}

	// Priority 3: Derive API URL from explorer URL using common patterns
	// This works with any network without hardcoding specific chain IDs
	explorerURL := network.Explorer
	if explorerURL == "" {
		return ""
	}

	// Convert explorer URL to API URL using common patterns
	if strings.Contains(explorerURL, "etherscan.io") {
		return strings.Replace(explorerURL, "https://", "https://api.", 1) + "/api"
	} else if strings.Contains(explorerURL, "polygonscan.com") {
		return strings.Replace(explorerURL, "https://", "https://api.", 1) + "/api"
	} else if strings.Contains(explorerURL, "arbiscan.io") {
		return strings.Replace(explorerURL, "https://", "https://api.", 1) + "/api"
	} else if strings.Contains(explorerURL, "bscscan.com") {
		return strings.Replace(explorerURL, "https://", "https://api.", 1) + "/api"
	} else if strings.Contains(explorerURL, "optimistic.etherscan.io") {
		return strings.Replace(explorerURL, "https://optimistic.", "https://api-optimistic.", 1) + "/api"
	} else if strings.Contains(explorerURL, "snowtrace.io") {
		return strings.Replace(explorerURL, "https://", "https://api.", 1) + "/api"
	}

	// For unknown explorers, try the most common pattern
	return strings.Replace(explorerURL, "https://", "https://api.", 1) + "/api"
}

// GetPromptContext provides ABI context for LLM prompts
func (a *ABIResolver) GetPromptContext(ctx context.Context, baggage map[string]interface{}) string {
	resolvedContracts, ok := baggage["resolved_contracts"].(map[string]*ContractInfo)
	if !ok || len(resolvedContracts) == 0 {
		return ""
	}

	var contextParts []string
	contextParts = append(contextParts, "### Verified Contract Information:")

	// Track detailed event information
	var eventDetails []string

	for address, contract := range resolvedContracts {
		if contract.IsVerified {
			var contractInfo []string

			// Contract address and name with type context
			if contract.IsImplementation {
				// This is an implementation contract
				if contract.ContractName != "" {
					contractInfo = append(contractInfo, fmt.Sprintf("Implementation Contract: %s (%s)", address, contract.ContractName))
				} else {
					contractInfo = append(contractInfo, fmt.Sprintf("Implementation Contract: %s", address))
				}
				if contract.ProxyAddress != "" {
					contractInfo = append(contractInfo, fmt.Sprintf("Used by Proxy: %s", contract.ProxyAddress))
				}
			} else if contract.IsProxy {
				// This is a proxy contract
				if contract.ContractName != "" {
					contractInfo = append(contractInfo, fmt.Sprintf("Proxy Contract: %s (%s)", address, contract.ContractName))
				} else {
					contractInfo = append(contractInfo, fmt.Sprintf("Proxy Contract: %s", address))
				}
				if contract.Implementation != "" {
					contractInfo = append(contractInfo, fmt.Sprintf("Implementation: %s", contract.Implementation))
				}
			} else {
				// Regular contract
				if contract.ContractName != "" {
					contractInfo = append(contractInfo, fmt.Sprintf("Contract: %s (%s)", address, contract.ContractName))
				} else {
					contractInfo = append(contractInfo, fmt.Sprintf("Contract: %s", address))
				}
			}

			// Contract verification status
			contractInfo = append(contractInfo, "Status: Verified on Etherscan")

			if contract.CompilerVersion != "" {
				contractInfo = append(contractInfo, fmt.Sprintf("Compiler: %s", contract.CompilerVersion))
			}

			// ABI information with detailed event parameters
			if len(contract.ParsedABI) > 0 {
				var functions, events []string
				for _, method := range contract.ParsedABI {
					if method.Type == "function" && method.Name != "" {
						functions = append(functions, method.Name)
					} else if method.Type == "event" && method.Name != "" {
						events = append(events, method.Name)

						// Build detailed event parameter information
						eventDetail := fmt.Sprintf("%s(", method.Name)
						var paramStrings []string
						for _, input := range method.Inputs {
							paramType := input.Type
							paramName := input.Name
							if input.Indexed {
								paramType = "indexed " + paramType
							}
							if paramName != "" {
								paramStrings = append(paramStrings, fmt.Sprintf("%s %s", paramType, paramName))
							} else {
								paramStrings = append(paramStrings, paramType)
							}
						}
						eventDetail += strings.Join(paramStrings, ", ") + ")"

						// Create clear contract description for event source
						contractDesc := contract.ContractName
						if contract.IsImplementation {
							contractDesc = fmt.Sprintf("%s (Implementation)", contract.ContractName)
						} else if contract.IsProxy {
							contractDesc = fmt.Sprintf("%s (Proxy)", contract.ContractName)
						}
						if contractDesc == "" {
							if contract.IsImplementation {
								contractDesc = "Implementation Contract"
							} else if contract.IsProxy {
								contractDesc = "Proxy Contract"
							} else {
								contractDesc = "Contract"
							}
						}

						eventDetails = append(eventDetails, fmt.Sprintf("- %s on %s: %s", method.Name, contractDesc, eventDetail))
					}
				}

				if len(functions) > 0 {
					// Show first few functions to avoid overwhelming the prompt
					displayFunctions := functions
					if len(functions) > 8 {
						displayFunctions = functions[:8]
						displayFunctions = append(displayFunctions, fmt.Sprintf("...and %d more", len(functions)-8))
					}
					contractInfo = append(contractInfo, fmt.Sprintf("Functions: %s", strings.Join(displayFunctions, ", ")))
				}

				if len(events) > 0 {
					// Show first few events
					displayEvents := events
					if len(events) > 6 {
						displayEvents = events[:6]
						displayEvents = append(displayEvents, fmt.Sprintf("...and %d more", len(events)-6))
					}
					contractInfo = append(contractInfo, fmt.Sprintf("Events: %s", strings.Join(displayEvents, ", ")))
				}
			}

			// Add to context with proper formatting
			contextParts = append(contextParts, "- "+strings.Join(contractInfo, "\n  "))
		}
	}

	if len(contextParts) == 1 {
		return "" // No verified contracts
	}

	// Add detailed event parameter information
	if len(eventDetails) > 0 {
		contextParts = append(contextParts, "", "### Event Parameter Details:")
		contextParts = append(contextParts, "Use these parameter names to extract specific information from events:")
		contextParts = append(contextParts, strings.Join(eventDetails, "\n"))
	}

	// Add proxy-implementation guidance if relevant
	hasProxies := false
	hasImplementations := false
	for _, contract := range resolvedContracts {
		if contract.IsProxy {
			hasProxies = true
		}
		if contract.IsImplementation {
			hasImplementations = true
		}
	}
	if hasProxies && hasImplementations {
		contextParts = append(contextParts, "", "### Proxy Contract Architecture:")
		contextParts = append(contextParts, "- Proxy contracts delegate calls to implementation contracts")
		contextParts = append(contextParts, "- Events and functions are typically defined in the implementation contract")
		contextParts = append(contextParts, "- Users interact with the proxy address, but the logic comes from the implementation")
		contextParts = append(contextParts, "- When describing transactions, focus on the proxy address that users interact with")
	}

	contextParts = append(contextParts, "", "Note: Contract names from Etherscan verification are authoritative. Use verified contract names to distinguish between token contracts (e.g., 'USDC', 'DAI') and protocol contracts (e.g., 'AggregationRouterV6', 'UniswapV2Router02').")

	return strings.Join(contextParts, "\n")
}

// looksLikeAddress checks if a string looks like an Ethereum address
func (a *ABIResolver) looksLikeAddress(addr string) bool {
	if addr == "" {
		return false
	}

	// Remove 0x prefix for length check
	cleanAddr := addr
	if strings.HasPrefix(addr, "0x") {
		cleanAddr = addr[2:]
	}

	// Address should be 40 hex chars (20 bytes) or 64 hex chars (padded)
	if len(cleanAddr) != 40 && len(cleanAddr) != 64 {
		return false
	}

	// Check if it's valid hex
	for _, char := range cleanAddr {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}

	return true
}

// cleanAddress removes padding from addresses and validates format
func (a *ABIResolver) cleanAddress(address string) string {
	if address == "" {
		return ""
	}

	// Remove 0x prefix for processing
	addr := address
	hasPrefix := strings.HasPrefix(addr, "0x")
	if hasPrefix {
		addr = addr[2:]
	}

	// If it's a padded 64-character address, extract the last 40 characters
	if len(addr) == 64 {
		addr = addr[24:] // Remove padding, keep last 40 chars
	}

	// Validate it's a proper hex address (40 characters)
	if len(addr) != 40 {
		return ""
	}

	// Re-add prefix and return
	return "0x" + addr
}

// GetResolvedContract is a helper function to get resolved contract from baggage
func GetResolvedContract(baggage map[string]interface{}, address string) (*ContractInfo, bool) {
	if contractsMap, ok := baggage["resolved_contracts"].(map[string]*ContractInfo); ok {
		contract, exists := contractsMap[strings.ToLower(address)]
		return contract, exists
	}
	return nil, false
}
