package tools

import (
	"context"
	"fmt"
)

// TransactionContextProvider provides basic transaction metadata context
type TransactionContextProvider struct {
	verbose bool
}

// NewTransactionContextProvider creates a new transaction context provider
func NewTransactionContextProvider() *TransactionContextProvider {
	return &TransactionContextProvider{
		verbose: false,
	}
}

// SetVerbose enables or disables verbose logging
func (t *TransactionContextProvider) SetVerbose(verbose bool) {
	t.verbose = verbose
}

// Name returns the tool name
func (t *TransactionContextProvider) Name() string {
	return "transaction_context_provider"
}

// Description returns the tool description
func (t *TransactionContextProvider) Description() string {
	return "Provides basic transaction metadata context (sender, recipient, gas, status)"
}

// Dependencies returns the tools this processor depends on
func (t *TransactionContextProvider) Dependencies() []string {
	return []string{} // No dependencies - processes raw transaction data
}

// Process extracts basic transaction context from raw data
func (t *TransactionContextProvider) Process(ctx context.Context, baggage map[string]interface{}) error {
	if t.verbose {
		fmt.Println("=== TRANSACTION CONTEXT PROVIDER: Processing ===")
	}

	// Process raw transaction receipt data
	rawData, ok := baggage["raw_data"].(map[string]interface{})
	if !ok {
		if t.verbose {
			fmt.Println("No raw transaction data found in baggage")
		}
		return nil
	}

	// Extract transaction context for other tools to use
	transactionContext := make(map[string]interface{})

	if receipt, ok := rawData["receipt"].(map[string]interface{}); ok {
		if from, ok := receipt["from"].(string); ok {
			transactionContext["sender"] = from
		}
		if to, ok := receipt["to"].(string); ok {
			transactionContext["recipient"] = to
		}
		if gasUsed, ok := receipt["gasUsed"].(string); ok {
			transactionContext["gas_used"] = gasUsed
		}
		if status, ok := receipt["status"].(string); ok {
			transactionContext["status"] = status
		}
	}

	// Store in baggage for other tools
	baggage["transaction_context"] = transactionContext

	if t.verbose {
		fmt.Printf("Extracted transaction context: %+v\n", transactionContext)
		fmt.Println("=== TRANSACTION CONTEXT PROVIDER: Complete ===")
	}

	return nil
}

// GetPromptContext provides transaction context for LLM prompts
func (t *TransactionContextProvider) GetPromptContext(ctx context.Context, baggage map[string]interface{}) string {
	// Get transaction context from baggage
	transactionContext, ok := baggage["transaction_context"].(map[string]interface{})
	if !ok || len(transactionContext) == 0 {
		return ""
	}

	context := "### TRANSACTION CONTEXT:"

	// Always include transaction sender prominently
	if sender, ok := transactionContext["sender"].(string); ok {
		context += fmt.Sprintf("\n- TRANSACTION SENDER: %s (the address that initiated this transaction)", sender)
	}

	if recipient, ok := transactionContext["recipient"].(string); ok {
		context += fmt.Sprintf("\n- Contract Called: %s", recipient)
	}

	if gasUsed, ok := transactionContext["gas_used"].(string); ok {
		context += fmt.Sprintf("\n- Total Gas Used: %s", gasUsed)
	}

	if status, ok := transactionContext["status"].(string); ok {
		statusFormatted := t.formatStatus(status)
		context += fmt.Sprintf("\n- Status: %s", statusFormatted)
	}

	return context
}

// formatStatus formats the transaction status for display
func (t *TransactionContextProvider) formatStatus(status string) string {
	switch status {
	case "0x1":
		return "Success"
	case "0x0":
		return "Failed"
	default:
		return status
	}
}
