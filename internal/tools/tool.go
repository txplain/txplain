package tools

import (
	"context"
)

// Tool is a unit of work for LangChainGo agents
type Tool interface {
	Name() string
	Description() string
	Run(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error)
}

// ContextProvider allows tools to provide additional context for LLM prompts
type ContextProvider interface {
	GetPromptContext(ctx context.Context, data map[string]interface{}) string
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