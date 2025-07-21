# Txplain

**Txplain** is an AI-powered blockchain transaction explanation service that transforms complex blockchain transaction data into human-readable summaries. Built with a **RPC-first architecture**, it uses direct blockchain calls and contract introspection to provide the most accurate and comprehensive transaction analysis possible.

## 🚀 **RPC-First Architecture** 

Txplain relies heavily on direct RPC calls to blockchain networks rather than hard-coded mappings:

- **🔍 Dynamic Contract Introspection**: Automatically detects ERC20, ERC721, ERC1155 tokens via `supportsInterface()` calls
- **📝 Real-time Signature Resolution**: Uses 4byte.directory API as fallback for unknown function/event signatures  
- **💰 Live Token Metadata**: Fetches name, symbol, decimals directly from contracts via `name()`, `symbol()`, `decimals()`
- **🧠 Context-Aware Analysis**: Contract type detection informs better transaction interpretation
- **⚡ Minimal External Dependencies**: Only uses external APIs when RPC calls aren't sufficient

## Features

- 🧠 **AI-Powered Analysis**: Uses OpenAI's GPT-4 with RPC-enhanced context for accurate explanations
- 🌐 **Multi-Network Support**: Ethereum (1), Polygon (137), and Arbitrum (42161) with network-specific optimizations
- 🔍 **Deep RPC Analysis**: Live contract introspection, signature resolution, and token metadata fetching
- 📊 **Structured Output**: Provides wallet effects, token transfers, and risk assessment
- 🌐 **Dual Interface**: REST API and Model Context Protocol (MCP) server
- 🏷️ **Smart Tagging**: Automatically categorizes transactions based on actual contract interactions
- 🔗 **Explorer Links**: Includes links to blockchain explorers for easy verification

## RPC-Enhanced Capabilities

### Contract Introspection
- **ERC Standard Detection**: Uses `supportsInterface(bytes4)` to identify ERC165, ERC721, ERC1155
- **ERC20 Detection**: Falls back to method availability checking for non-ERC165 compliant tokens  
- **Token Metadata**: Direct calls to `name()`, `symbol()`, `decimals()`, `totalSupply()`
- **NFT Information**: Supports `ownerOf()`, `tokenURI()` for comprehensive NFT analysis

### Signature Resolution
- **Function Signatures**: Resolves method signatures with local database + 4byte.directory fallback
- **Event Signatures**: Comprehensive event signature database with topic0 resolution
- **ABI Decoding**: Enhanced argument parsing based on resolved signatures
- **Contract Context**: Function interpretation enhanced by contract type knowledge

### Examples of RPC Enhancement

```json
// Instead of hard-coded mappings, Txplain uses live RPC calls:

// ❌ OLD: Hard-coded approach
{
  "method": "0xa9059cbb",
  "contract": "0x...",
  "arguments": {"raw_data": "0x..."}
}

// ✅ NEW: RPC-enhanced approach  
{
  "method": "transfer",
  "signature": "transfer(address,uint256)",
  "contract": "0xA0b86a33E6411E884D578FD4FF4A5DFCB",
  "contract_type": "ERC20",
  "contract_name": "USD Coin",
  "contract_symbol": "USDC",
  "contract_decimals": 6,
  "arguments": {
    "to": "0xRecipientAddress",
    "amount": "100000000",
    "amount_decimal": 100000000
  }
}
```

## Quick Start

### Prerequisites

- Go 1.23.0 or later  
- OpenAI API key
- Internet connection for blockchain RPC calls

### Installation

```bash
# Clone the repository
git clone https://github.com/your-username/txplain.git
cd txplain

# Install dependencies
go mod download

# Build the application
go build -o txplain cmd/main.go
```

### Running the Service

```bash
# Set your OpenAI API key
export OPENAI_API_KEY="your-openai-api-key-here"

# Run with default settings (HTTP on :8080, MCP on :8081)
./txplain

# Or specify custom ports and API key
./txplain -http-addr=":3000" -mcp-addr=":3001" -openai-key="your-key"

# Run only HTTP API server
./txplain -mcp=false

# Run only MCP server
./txplain -http=false
```

## API Reference

### REST API

The HTTP API runs on port 8080 by default and provides the following endpoints:

#### Health Check

```http
GET /health
```

**Response:**
```json
{
  "status": "healthy",
  "timestamp": "2024-01-01T00:00:00Z",
  "service": "txplain",
  "version": "1.0.0"
}
```

#### Get Supported Networks

```http
GET /api/v1/networks
```

#### Explain Transaction (RPC-Enhanced)

```http
POST /api/v1/explain
Content-Type: application/json

{
  "tx_hash": "0x123...",
  "network_id": 1
}
```

**Enhanced Response with RPC Data:**
```json
{
  "tx_hash": "0x123...",
  "network_id": 1,
  "summary": "This transaction swapped 100 USDC for ETH on Uniswap V2...",
  "effects": [
    {
      "address": "0xabc...",
      "net_change": "-100000000",
      "transfers": [
        {
          "type": "ERC20",
          "contract": "0xA0b86a33E6411...", 
          "from": "0xabc...",
          "to": "0xdef...",
          "amount": "100000000",
          "symbol": "USDC",        // ← Fetched via RPC
          "name": "USD Coin",      // ← Fetched via RPC
          "decimals": 6            // ← Fetched via RPC
        }
      ],
      "gas_spent": "150000"
    }
  ],
  "metadata": {
    "contracts": {                 // ← RPC-derived contract info
      "0xA0b86a33E6411...": {
        "type": "ERC20",
        "name": "USD Coin", 
        "symbol": "USDC"
      }
    }
  },
  "links": {
    "transaction": "https://etherscan.io/tx/0x123...",
    "0xA0b86a33E6411...": "https://etherscan.io/address/0xA0b86a33E6411..."
  },
  "tags": ["defi", "swap", "token-transfer"]
}
```

## Architecture

The service is built with a **RPC-first modular architecture**:

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   HTTP/MCP      │    │    Agent         │    │  Blockchain     │
│   Servers       │───▶│  Orchestrator    │───▶│  RPC Clients    │
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

### Key RPC Components

- **Contract Introspection**: `internal/rpc/contract.go` - Live contract analysis
- **Signature Resolution**: `internal/rpc/signature.go` - Dynamic signature resolution  
- **Enhanced Tools**: RPC-aware trace and log decoders
- **Agent Orchestration**: Coordinates RPC calls with AI analysis

## RPC vs External APIs

Txplain minimizes external API dependencies by prioritizing RPC calls:

| Data Type | RPC Method | External Fallback | 
|-----------|------------|------------------|
| Token Name | `name()` | None needed |
| Token Symbol | `symbol()` | None needed |
| Token Decimals | `decimals()` | None needed |
| Contract Type | `supportsInterface()` | None needed |
| Function Signatures | Local database | 4byte.directory |
| Event Signatures | Local database | None currently |
| Token Prices | None | CoinMarketCap (if needed) |
| NFT Metadata | `tokenURI()` | Alchemy NFT API (if needed) |

## Configuration

### Environment Variables

- `OPENAI_API_KEY`: Your OpenAI API key (required)
- `ENV`: Set to "development" to include detailed error messages

### Command Line Flags

- `-http-addr`: HTTP server address (default: ":8080")
- `-mcp-addr`: MCP server address (default: ":8081") 
- `-openai-key`: OpenAI API key (overrides env var)
- `-http`: Enable HTTP server (default: true)
- `-mcp`: Enable MCP server (default: true)
- `-version`: Show version and exit

## Supported Networks & RPC Features

| Network ID | Name      | Trace Support | Contract Calls | Features |
|------------|-----------|---------------|----------------|----------|
| 1          | Ethereum  | `debug_traceTransaction` | ✅ Full | ERC detection, token metadata |
| 137        | Polygon   | `debug_traceTransaction` | ✅ Full | ERC detection, token metadata |
| 42161      | Arbitrum  | `arbtrace_transaction` | ✅ Full | ERC detection, token metadata |

## Development

### Project Structure

```
txplain/
├── cmd/main.go                   # 🎯 Application entry point
├── internal/
│   ├── models/types.go           # 📊 Data structures & network configs
│   ├── rpc/                      # 🔗 RPC-first blockchain integration
│   │   ├── client.go             # Core RPC client
│   │   ├── contract.go           # 🔍 Contract introspection
│   │   └── signature.go          # 📝 Signature resolution
│   ├── tools/                    # 🛠️ RPC-enhanced analysis tools
│   │   ├── trace_decoder.go      # 📋 RPC-aware trace analysis
│   │   ├── log_decoder.go        # 📝 RPC-aware event analysis
│   │   └── transaction_explainer.go # 🧠 AI explanations
│   ├── agent/agent.go            # 🎭 RPC-enhanced orchestration
│   ├── api/server.go             # 🌐 REST API server
│   └── mcp/server.go             # 🔌 MCP protocol server
├── README.md                     # 📚 This comprehensive guide
├── example.env                   # ⚙️ Configuration template
└── txplain                       # 📦 Compiled binary
```

### Example Transaction Analysis Flow

1. **RPC Data Fetch**: Transaction, receipt, trace, block data
2. **Contract Introspection**: Detect ERC standards via `supportsInterface()`
3. **Token Metadata**: Fetch name/symbol/decimals via direct calls  
4. **Signature Resolution**: Resolve unknown signatures via 4byte.directory
5. **Enhanced Decoding**: Parse calls/events with full context
6. **AI Enhancement**: LLM generates explanation with RPC-enriched data

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

This project is licensed under the MIT License - see the LICENSE file for details.

## Acknowledgments

- [LangChainGo](https://github.com/tmc/langchaingo) for the LLM integration framework
- [Gorilla Mux](https://github.com/gorilla/mux) for HTTP routing
- [4byte.directory](https://4byte.directory) for function signature resolution
- OpenAI for providing the GPT-4 model
- The Ethereum community for comprehensive RPC documentation

## Support

For questions, issues, or contributions, please visit our [GitHub repository](https://github.com/your-username/txplain) or contact the maintainers.

---

**Txplain** - RPC-powered blockchain transaction analysis that actually understands your contracts! 🚀⚡
