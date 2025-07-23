package tools

import (
	"context"
)

// RagContextItem represents a piece of knowledge for RAG storage and retrieval
type RagContextItem struct {
	ID        string                 `json:"id"`        // Unique identifier for vector storage
	Type      string                 `json:"type"`      // Category: "token", "protocol", "address", etc.
	Title     string                 `json:"title"`     // Short description for retrieval
	Content   string                 `json:"content"`   // Main text content to embed
	Metadata  map[string]interface{} `json:"metadata"`  // Structured data for filtering
	Keywords  []string               `json:"keywords"`  // Keywords for retrieval enhancement
	Relevance float64                `json:"relevance"` // Base relevance score (0-1)
}

// RagContext holds multiple knowledge items for RAG storage
type RagContext struct {
	Items []RagContextItem `json:"items"`
}

// NewRagContext creates a new RAG context
func NewRagContext() *RagContext {
	return &RagContext{
		Items: make([]RagContextItem, 0),
	}
}

// AddItem adds a knowledge item to the RAG context
func (rc *RagContext) AddItem(item RagContextItem) {
	rc.Items = append(rc.Items, item)
}

// Tool is the unified interface that combines baggage processing, prompt context, and RAG context
// This replaces the separate BaggageProcessor and ContextProvider interfaces
type Tool interface {
	// Core tool identification
	Name() string
	Description() string

	// Processing and dependencies
	Dependencies() []string // Names of tools this tool depends on
	Process(ctx context.Context, baggage map[string]interface{}) error

	// Context provision for LLMs
	GetPromptContext(ctx context.Context, baggage map[string]interface{}) string

	// RAG context provision for vector storage and retrieval
	// This should return knowledge that can be:
	// 1. Embedded into vector databases
	// 2. Retrieved based on query similarity
	// 3. Selectively included in prompts based on relevance
	GetRagContext(ctx context.Context, baggage map[string]interface{}) *RagContext
}

// ToolError represents an error that occurred during tool execution
type ToolError struct {
	Tool    string `json:"tool"`
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

func (e ToolError) Error() string {
	return e.Message
}

// NewToolError creates a new tool error
func NewToolError(tool, message, code string) *ToolError {
	return &ToolError{
		Tool:    tool,
		Message: message,
		Code:    code,
	}
}
