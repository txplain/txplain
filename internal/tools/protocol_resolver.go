package tools

import (
	"context"
	"strings"

	"github.com/txplain/txplain/internal/models"
)

// ProtocolResolver identifies DeFi protocols from contract addresses and patterns
type ProtocolResolver struct{}

// NewProtocolResolver creates a new protocol resolver
func NewProtocolResolver() *ProtocolResolver {
	return &ProtocolResolver{}
}

// Name returns the tool name
func (p *ProtocolResolver) Name() string {
	return "protocol_resolver"
}

// Description returns the tool description
func (p *ProtocolResolver) Description() string {
	return "Identifies DeFi protocols (Uniswap, 1inch, etc.) from contract addresses and transaction patterns"
}

// Dependencies returns the tools this processor depends on
func (p *ProtocolResolver) Dependencies() []string {
	return []string{"abi_resolver", "token_transfer_extractor"}
}

// ProtocolInfo represents detected protocol information
type ProtocolInfo struct {
	Name      string   `json:"name"`
	Type      string   `json:"type"` // DEX, Aggregator, Lending, etc.
	Version   string   `json:"version,omitempty"`
	Contracts []string `json:"contracts"`
}

// Process identifies protocols from transaction data
func (p *ProtocolResolver) Process(ctx context.Context, baggage map[string]interface{}) error {
	protocols := p.detectProtocols(baggage)
	baggage["protocols"] = protocols
	return nil
}

// detectProtocols identifies protocols from transaction data
func (p *ProtocolResolver) detectProtocols(baggage map[string]interface{}) []ProtocolInfo {
	var protocols []ProtocolInfo
	contractsFound := make(map[string]bool)

	// First, try to identify protocols using resolved contract information
	if resolvedContracts, ok := baggage["resolved_contracts"].(map[string]*ContractInfo); ok {
		for _, contractInfo := range resolvedContracts {
			if contractInfo.IsVerified {
				if protocol := p.identifyProtocolFromContractInfo(contractInfo); protocol != nil {
					protocolKey := protocol.Name + "_" + protocol.Type
					if !contractsFound[protocolKey] {
						protocols = append(protocols, *protocol)
						contractsFound[protocolKey] = true
					}
				}
			}
		}
	}

	// Get all contract addresses from transfers and events
	contracts := p.extractContracts(baggage)

	// Check each contract against known protocol addresses (fallback)
	for _, contract := range contracts {
		contractLower := strings.ToLower(contract)

		if protocol := p.identifyProtocolByAddress(contractLower); protocol != nil {
			// Avoid duplicates
			protocolKey := protocol.Name + "_" + protocol.Type
			if !contractsFound[protocolKey] {
				protocols = append(protocols, *protocol)
				contractsFound[protocolKey] = true
			}
		}
	}

	return protocols
}

// extractContracts gets all contract addresses from transaction data
func (p *ProtocolResolver) extractContracts(baggage map[string]interface{}) []string {
	var contracts []string
	contractMap := make(map[string]bool)

	// From transfers
	if transfers, ok := baggage["transfers"].([]models.TokenTransfer); ok {
		for _, transfer := range transfers {
			if transfer.Contract != "" && !contractMap[transfer.Contract] {
				contracts = append(contracts, transfer.Contract)
				contractMap[transfer.Contract] = true
			}
			// Also check from/to addresses that might be protocol contracts
			if transfer.From != "" && !contractMap[transfer.From] {
				contracts = append(contracts, transfer.From)
				contractMap[transfer.From] = true
			}
			if transfer.To != "" && !contractMap[transfer.To] {
				contracts = append(contracts, transfer.To)
				contractMap[transfer.To] = true
			}
		}
	}

	// From events
	if events, ok := baggage["events"].([]models.Event); ok {
		for _, event := range events {
			if event.Contract != "" && !contractMap[event.Contract] {
				contracts = append(contracts, event.Contract)
				contractMap[event.Contract] = true
			}
		}
	}

	// From raw transaction data
	if rawData, ok := baggage["raw_data"].(map[string]interface{}); ok {
		if receipt, ok := rawData["receipt"].(map[string]interface{}); ok {
			if to, ok := receipt["to"].(string); ok && to != "" && !contractMap[to] {
				contracts = append(contracts, to)
				contractMap[to] = true
			}
		}
	}

	return contracts
}

// identifyProtocolByAddress identifies a protocol by its contract address
func (p *ProtocolResolver) identifyProtocolByAddress(address string) *ProtocolInfo {
	// Known protocol addresses (Ethereum mainnet)
	knownProtocols := map[string]ProtocolInfo{
		// 1inch
		"0x111111125421ca6dc452d289314280a0f8842a65": {
			Name:    "1inch",
			Type:    "Aggregator",
			Version: "v6",
		},
		"0x1111111254eeb25477b68fb85ed929f73a960582": {
			Name:    "1inch",
			Type:    "Aggregator",
			Version: "v5",
		},

		// Uniswap V3
		"0xe592427a0aece92de3edee1f18e0157c05861564": {
			Name:    "Uniswap",
			Type:    "DEX",
			Version: "v3",
		},

		// Uniswap V2
		"0x7a250d5630b4cf539739df2c5dacb4c659f2488d": {
			Name:    "Uniswap",
			Type:    "DEX",
			Version: "v2",
		},

		// Common token contracts (for context)
		"0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2": {
			Name:    "Wrapped Ether",
			Type:    "Token",
			Version: "WETH",
		},
		"0xdac17f958d2ee523a2206206994597c13d831ec7": {
			Name:    "Tether USD",
			Type:    "Token",
			Version: "USDT",
		},

		// Add more as needed...
	}

	if protocol, exists := knownProtocols[address]; exists {
		protocol.Contracts = []string{address}
		return &protocol
	}

	return nil
}

// identifyProtocolFromContractInfo identifies a protocol from resolved contract information
func (p *ProtocolResolver) identifyProtocolFromContractInfo(contractInfo *ContractInfo) *ProtocolInfo {
	// Check contract name for protocol patterns
	contractName := strings.ToLower(contractInfo.ContractName)
	sourceCode := strings.ToLower(contractInfo.SourceCode)

	// Uniswap patterns
	if strings.Contains(contractName, "uniswap") || strings.Contains(sourceCode, "uniswap") {
		version := "unknown"
		if strings.Contains(contractName, "v2") || strings.Contains(sourceCode, "uniswapv2") {
			version = "v2"
		} else if strings.Contains(contractName, "v3") || strings.Contains(sourceCode, "uniswapv3") {
			version = "v3"
		} else if strings.Contains(contractName, "v4") || strings.Contains(sourceCode, "uniswapv4") {
			version = "v4"
		}

		protocolType := "DEX"
		if strings.Contains(contractName, "router") || strings.Contains(sourceCode, "swaprouter") {
			protocolType = "DEX Router"
		} else if strings.Contains(contractName, "factory") {
			protocolType = "DEX Factory"
		} else if strings.Contains(contractName, "pair") {
			protocolType = "DEX Pool"
		}

		return &ProtocolInfo{
			Name:      "Uniswap",
			Type:      protocolType,
			Version:   version,
			Contracts: []string{contractInfo.Address},
		}
	}

	// 1inch patterns
	if strings.Contains(contractName, "1inch") || strings.Contains(sourceCode, "1inch") ||
		strings.Contains(contractName, "aggregation") && strings.Contains(contractName, "router") {
		version := "unknown"
		if strings.Contains(contractName, "v6") {
			version = "v6"
		} else if strings.Contains(contractName, "v5") {
			version = "v5"
		} else if strings.Contains(contractName, "v4") {
			version = "v4"
		}

		return &ProtocolInfo{
			Name:      "1inch",
			Type:      "Aggregator",
			Version:   version,
			Contracts: []string{contractInfo.Address},
		}
	}

	// SushiSwap patterns
	if strings.Contains(contractName, "sushiswap") || strings.Contains(sourceCode, "sushiswap") ||
		strings.Contains(contractName, "sushi") {
		protocolType := "DEX"
		if strings.Contains(contractName, "router") {
			protocolType = "DEX Router"
		} else if strings.Contains(contractName, "factory") {
			protocolType = "DEX Factory"
		}

		return &ProtocolInfo{
			Name:      "SushiSwap",
			Type:      protocolType,
			Contracts: []string{contractInfo.Address},
		}
	}

	// Curve patterns
	if strings.Contains(contractName, "curve") || strings.Contains(sourceCode, "curve") {
		protocolType := "DEX"
		if strings.Contains(contractName, "pool") {
			protocolType = "DEX Pool"
		} else if strings.Contains(contractName, "factory") {
			protocolType = "DEX Factory"
		}

		return &ProtocolInfo{
			Name:      "Curve",
			Type:      protocolType,
			Contracts: []string{contractInfo.Address},
		}
	}

	// Balancer patterns
	if strings.Contains(contractName, "balancer") || strings.Contains(sourceCode, "balancer") {
		protocolType := "DEX"
		if strings.Contains(contractName, "vault") {
			protocolType = "DEX Vault"
		} else if strings.Contains(contractName, "pool") {
			protocolType = "DEX Pool"
		}

		return &ProtocolInfo{
			Name:      "Balancer",
			Type:      protocolType,
			Contracts: []string{contractInfo.Address},
		}
	}

	// Aave patterns
	if strings.Contains(contractName, "aave") || strings.Contains(sourceCode, "aave") {
		protocolType := "Lending"
		if strings.Contains(contractName, "pool") {
			protocolType = "Lending Pool"
		} else if strings.Contains(contractName, "atoken") {
			protocolType = "Lending Token"
		}

		return &ProtocolInfo{
			Name:      "Aave",
			Type:      protocolType,
			Contracts: []string{contractInfo.Address},
		}
	}

	// Compound patterns
	if strings.Contains(contractName, "compound") || strings.Contains(sourceCode, "compound") ||
		strings.Contains(contractName, "ctoken") {
		return &ProtocolInfo{
			Name:      "Compound",
			Type:      "Lending",
			Contracts: []string{contractInfo.Address},
		}
	}

	// OpenSea patterns
	if strings.Contains(contractName, "opensea") || strings.Contains(sourceCode, "opensea") ||
		strings.Contains(contractName, "seaport") {
		return &ProtocolInfo{
			Name:      "OpenSea",
			Type:      "NFT Marketplace",
			Contracts: []string{contractInfo.Address},
		}
	}

	// Generic DEX patterns
	if (strings.Contains(contractName, "swap") || strings.Contains(sourceCode, "swap")) &&
		(strings.Contains(contractName, "router") || strings.Contains(sourceCode, "router")) {
		return &ProtocolInfo{
			Name:      "Unknown DEX",
			Type:      "DEX Router",
			Contracts: []string{contractInfo.Address},
		}
	}

	// ERC20 tokens (for context)
	if contractInfo.ParsedABI != nil {
		hasTransfer := false
		hasApprove := false
		for _, method := range contractInfo.ParsedABI {
			if method.Name == "transfer" {
				hasTransfer = true
			}
			if method.Name == "approve" {
				hasApprove = true
			}
		}
		if hasTransfer && hasApprove {
			name := contractInfo.ContractName
			if name == "" {
				name = "Unknown Token"
			}
			return &ProtocolInfo{
				Name:      name,
				Type:      "Token",
				Contracts: []string{contractInfo.Address},
			}
		}
	}

	return nil
}

// GetPromptContext provides protocol context for LLM prompts
func (p *ProtocolResolver) GetPromptContext(ctx context.Context, baggage map[string]interface{}) string {
	protocols, ok := baggage["protocols"].([]ProtocolInfo)
	if !ok || len(protocols) == 0 {
		return ""
	}

	context := "### Protocol Detection:\n"

	for _, protocol := range protocols {
		if protocol.Type != "Token" { // Skip token contracts
			protocolDesc := protocol.Name
			if protocol.Version != "" {
				protocolDesc += " " + protocol.Version
			}
			protocolDesc += " (" + protocol.Type + ")"

			context += "- " + protocolDesc + "\n"
		}
	}

	return context
}
