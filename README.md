# txplain

> Pronounce: Tex-plain

An open-source AI-powered blockchain transaction explanation service that transforms complex blockchain transaction data into human-readable summaries. Built with a **RPC-first architecture**, it uses direct blockchain calls and contract introspection to provide accurate and comprehensive transaction analysis.

![Screenshot](./web/public/screenshot.png)

## High-Level Architecture

Txplain uses a modular RPC-first design that prioritizes direct blockchain calls over hard-coded mappings:

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   CLI/HTTP/MCP  │    │    Agent         │    │  Blockchain     │
│   Interfaces    │───▶│  Orchestrator    │───▶│  RPC Clients    │
└─────────────────┘    └──────────────────┘    └─────────────────┘
                              │                        │
                              ▼                        │
                    ┌──────────────────┐               │
                    │  Enhanced Tools  │               │
                    │                  │               │
                    │ • RPC TraceDecoder◀──────────────┤
                    │ • RPC LogDecoder   │               │
                    │ • AI Explainer     │               │
                    └──────────────────┘               │
                              │                        │
                              ▼                        │
                    ┌──────────────────┐               │
                    │  RPC Services    │◀──────────────┘
                    │                  │
                    │ • Contract Info  │
                    │ • Signature Resolver │
                    │ • Token Metadata │
                    └──────────────────┘
```

### Key Features

- 🔍 **Dynamic Contract Introspection**: Automatically detects ERC20, ERC721, ERC1155 tokens via `supportsInterface()` calls
- 📝 **Real-time Signature Resolution**: Uses 4byte.directory API as fallback for unknown function/event signatures  
- 💰 **Live Token Metadata**: Fetches name, symbol, decimals directly from contracts
- 🧠 **AI-Powered Analysis**: Uses OpenAI's GPT-4 with RPC-enhanced context for accurate explanations
- 🌐 **Multi-Network Support**: Ethereum, Polygon, and Arbitrum
- ⚡ **Minimal External Dependencies**: Only uses external APIs when RPC calls aren't sufficient

## Architecture Principles

⚠️ **CRITICAL**: All contributors and AI assistants MUST follow these architecture principles when working on this codebase.

### Core Design Philosophy

Txplain follows a **clean, decoupled pipeline architecture** where each tool is completely isolated and communicates through well-defined interfaces. This ensures zero logic leaks, maximum testability, and easy extensibility.

### 🏗️ Tool Architecture Rules

**Each tool MUST follow these 4 principles:**

#### 1. **Dependencies Declaration** 
```go
func (t *MyTool) Dependencies() []string {
    return []string{"dependency1", "dependency2"} // Enforces execution order
}
```

#### 2. **Structured Data to Baggage**
```go
// ✅ GOOD: Add structured data for other tools' deterministic logic
baggage["my_structured_data"] = MyStructuredData{...}
```

#### 3. **Text Context via GetPromptContext()**  
```go
// ✅ GOOD: Provide LLM context for other tools' probabilistic logic
func (t *MyTool) GetPromptContext(ctx context.Context, baggage map[string]interface{}) string {
    return "### MY TOOL CONTEXT:\n- Useful context for LLM analysis"
}
```

#### 4. **Complete Tool Isolation**
```go
// ❌ FORBIDDEN: Never access baggage to build context from other tools
func (t *MyTool) buildContextFromOtherTools(baggage map[string]interface{}) string {
    // This violates isolation - DON'T DO THIS
}

// ✅ CORRECT: Use context from providers (same pattern as TransactionExplainer)
func (t *MyTool) Process(ctx context.Context, baggage map[string]interface{}) error {
    var additionalContext []string
    if contextProviders, ok := baggage["context_providers"].([]ContextProvider); ok {
        for _, provider := range contextProviders {
            if context := provider.GetPromptContext(ctx, baggage); context != "" {
                additionalContext = append(additionalContext, context)
            }
        }
    }
    contextData := strings.Join(additionalContext, "\n\n")
    // Use contextData for LLM calls
}
```

### 🔄 Context Flow Architecture

```mermaid
graph TD
    A[Raw Transaction Data] --> B[Tool 1: Extracts addresses]
    B --> C[Tool 2: Resolves ABIs] 
    C --> D[Tool 3: Decodes events]
    
    B --> E[Baggage: Structured Data]
    C --> E
    D --> E
    
    B --> F[GetPromptContext: LLM Text]
    C --> F  
    D --> F
    
    E --> G[Tool N: Uses structured data for logic]
    F --> H[Tool N: Uses LLM context for AI analysis]
    
    style E fill:#e3f2fd
    style F fill:#f3e5f5
    style G fill:#e8f5e8
    style H fill:#fff3e0
```

### 📦 Data Flow Patterns

#### ✅ **Correct Data Flow**

**For Deterministic Logic:**
```go
// Tool A produces structured data
baggage["token_transfers"] = []TokenTransfer{...}

// Tool B consumes structured data  
if transfers, ok := baggage["token_transfers"].([]TokenTransfer); ok {
    // Process transfers deterministically
}
```

**For LLM/AI Logic:**
```go
// Tool A provides context
func (a *ToolA) GetPromptContext(ctx context.Context, baggage map[string]interface{}) string {
    return "### TOKEN TRANSFERS:\n- USDT: 100 tokens transferred"
}

// Tool B uses context from providers
var additionalContext []string
if contextProviders, ok := baggage["context_providers"].([]ContextProvider); ok {
    for _, provider := range contextProviders {
        if context := provider.GetPromptContext(ctx, baggage); context != "" {
            additionalContext = append(additionalContext, context)
        }
    }
}
// Use additionalContext for LLM calls
```

### 🚫 Anti-Patterns (DO NOT DO)

#### ❌ **Hardcoded Logic**
```go
// DON'T: Hardcode specific event names or protocols
if eventName == "Approval" {
    // Hardcoded special case logic
}
```

#### ❌ **Direct Baggage Context Building** 
```go
// DON'T: Build context by directly accessing other tools' baggage data
func (t *MyTool) buildContext(baggage map[string]interface{}) string {
    if events, ok := baggage["events"].([]Event); ok {
        // This violates tool isolation
    }
}
```

#### ❌ **Logic Leaks Between Tools**
```go
// DON'T: Make tool behavior depend on internal details of other tools
if protocolName == "Uniswap" && version == "v3" {
    // This couples tools together
}
```

### 🛠️ Adding New Tools

When adding a new tool, follow this checklist:

1. **✅ Implement Required Interfaces**
   ```go
   type MyNewTool struct {
       // Tool state
   }
   
   func (t *MyNewTool) Name() string { return "my_new_tool" }
   func (t *MyNewTool) Dependencies() []string { return []string{"dependency1"} }
   func (t *MyNewTool) Process(ctx context.Context, baggage map[string]interface{}) error { ... }
   func (t *MyNewTool) GetPromptContext(ctx context.Context, baggage map[string]interface{}) string { ... }
   ```

2. **✅ Add to Pipeline in Agent**
   ```go
   myTool := txtools.NewMyNewTool()
   if err := pipeline.AddProcessor(myTool); err != nil {
       return nil, fmt.Errorf("failed to add my tool: %w", err)
   }
   contextProviders = append(contextProviders, myTool)
   ```

3. **✅ Use Generic, AI-Driven Logic Only**
   - Let the LLM handle classification and reasoning
   - Avoid hardcoding protocol names, event types, or special cases
   - Use structured data + context patterns exclusively

### 🧪 Testing New Tools

```go
// Test data flow
func TestMyToolDataFlow(t *testing.T) {
    tool := NewMyNewTool()
    baggage := map[string]interface{}{
        "input_data": testData,
    }
    
    // Test processing
    err := tool.Process(ctx, baggage)
    assert.NoError(t, err)
    
    // Test structured data output
    result, ok := baggage["my_output"].(MyOutputType)
    assert.True(t, ok)
    assert.NotEmpty(t, result)
    
    // Test context output
    context := tool.GetPromptContext(ctx, baggage)
    assert.Contains(t, context, "### MY TOOL CONTEXT:")
}
```

### 🎯 Benefits of This Architecture

- **🔄 Zero Coupling**: Tools can be added/removed/modified independently
- **🧪 100% Testable**: Each tool can be tested in isolation
- **🚀 Scalable**: New functionality doesn't break existing tools
- **🔍 Generic**: No hardcoded protocol or event logic anywhere
- **🧠 AI-Powered**: LLM handles all classification and reasoning
- **📦 Clean Interfaces**: Clear separation between structured data and LLM context

### 🚨 Enforcement

**Code reviewers and AI assistants MUST reject any code that:**
- Hardcodes protocol names, event types, or special cases
- Builds context by directly accessing other tools' baggage data  
- Creates dependencies between tools beyond the explicit dependency system
- Mixes deterministic logic with LLM context building

**This architecture enables the entire system to be generic, maintainable, and extensible.** 🎯

## Installation

### Prerequisites

- Go 1.23.0 or later  
- OpenAI API key

### Setup

```bash
# Clone the repository
git clone https://github.com/your-username/txplain.git
cd txplain

# Install dependencies
go mod download

# Create configuration file from template
cp example.env .env

# Edit .env file with your API keys and network configurations
# At minimum, set your OpenAI API key:
# OPENAI_API_KEY=your_openai_api_key_here

# Build the application
go build -o txplain cmd/main.go
```

## Configuration

### Network Configuration

Txplain supports flexible network configuration through environment variables. You can easily add support for new blockchain networks without modifying the code.

#### Environment Variable Patterns

```bash
# RPC Endpoints (Required)
RPC_ENDPOINT_CHAIN_<CHAIN_ID>=<RPC_URL>

# Network Names (Optional)
NETWORK_NAME_CHAIN_<CHAIN_ID>=<NETWORK_NAME>

# Explorer URLs (Optional)
EXPLORER_URL_CHAIN_<CHAIN_ID>=<EXPLORER_URL>
```

#### Default Networks

If no environment variables are configured, Txplain uses these default networks:

- **Ethereum (1)**: Built-in RPC endpoint with Etherscan explorer
- **Polygon (137)**: Built-in RPC endpoint with Polygonscan explorer  
- **Arbitrum (42161)**: Built-in RPC endpoint with Arbiscan explorer

#### Adding New Networks

To add support for a new blockchain network, simply add the environment variables to your `.env` file:

```bash
# Example: Add Binance Smart Chain
RPC_ENDPOINT_CHAIN_56=https://bsc-dataseed.binance.org
NETWORK_NAME_CHAIN_56=Binance Smart Chain
EXPLORER_URL_CHAIN_56=https://bscscan.com

# Example: Add Avalanche
RPC_ENDPOINT_CHAIN_43114=https://api.avax.network/ext/bc/C/rpc
NETWORK_NAME_CHAIN_43114=Avalanche
EXPLORER_URL_CHAIN_43114=https://snowtrace.io
```

### Configuration File

Create a `.env` file from the provided template and configure your API keys:

```bash
# Copy the example configuration
cp example.env .env

# Edit .env with your settings
```

**Required variables in `.env`:**
```bash
# OpenAI API key for transaction analysis
OPENAI_API_KEY=your_openai_api_key_here

# Etherscan API key for contract verification  
ETHERSCAN_API_KEY=your_etherscan_api_key_here

# Optional: CoinMarketCap API key for enhanced token pricing
# Enables both centralized (CEX) and decentralized (DEX) exchange data
COINMARKETCAP_API_KEY=your_coinmarketcap_api_key_here
```

## GUI Server

1. Run the code with -http flag:

    ```sh
    go run ./cmd/main.go -http
    ```

2. Visit `http://localhost:8080/`

## Examples

### Example 1: Token Approval Transaction

```bash
➜  txplain git:(main) go run ./cmd/main.go -tx 0x85a909e8d6d173768afa9dcb3116f88ecf25a8af884b078d02b3ad0a7167f998 -network 1 

Approved Uniswap v2 Router to spend unlimited PEPE tokens from 0x3286...399f (outta.eth).
```

### Example 2: Token Swap Transaction

```bash
➜  txplain git:(main) go run ./cmd/main.go -tx 0xed21b60a115828a7bdaaa6d22309e3a5ba47375b926d18fa8e5768a1d65458e0 -network 1   

Swapped 100 USDT ($100) for 57,071 GrowAI tokens via 1inch v6 aggregator with $1.02 gas fee.
```

### Example 3: Repaying Debt

```
➜  txplain git:(main) ✗ go run ./cmd/main.go -tx 0x715344c0f9a035577e221db859394cb301f577540475d2d3b1709deec605925d -network 1
Repaid 25 USDC debt and withdrew collateral on Curve for 0xea7b...1889 for 25 USDC + $0.55 gas.
```

### Additional Usage

```bash
# Run HTTP API server (default port 8080)
./txplain

# Run with custom ports
./txplain -http-addr=":3000" -mcp-addr=":3001"

# Run only HTTP server
./txplain -mcp=false
```

## Acknowledgments

- [LangChainGo](https://github.com/tmc/langchaingo) for the LLM integration framework
- [Gorilla Mux](https://github.com/gorilla/mux) for HTTP routing
- [4byte.directory](https://4byte.directory) for function signature resolution
- OpenAI for providing the GPT-4 model
- The Ethereum community for comprehensive RPC documentation

---

**Txplain** - RPC-powered blockchain transaction analysis that actually understands your contracts! 🚀⚡
