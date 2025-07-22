package tools

import (
	"context"
	"testing"

	"github.com/txplain/txplain/internal/models"
)

func TestLogDecoder_RobustABIParsing_ERC20Transfer(t *testing.T) {
	decoder := NewLogDecoder()
	
	// Example: ERC20 Transfer event with ABI
	// Transfer(address indexed from, address indexed to, uint256 value)
	abiMethod := &ABIMethod{
		Name: "Transfer",
		Type: "event", 
		Hash: "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
		Inputs: []ABIInput{
			{Name: "from", Type: "address", Indexed: true},
			{Name: "to", Type: "address", Indexed: true},
			{Name: "value", Type: "uint256", Indexed: false},
		},
	}
	
	topics := []string{
		"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef", // Transfer signature
		"0x000000000000000000000000a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0", // from address (padded)
		"0x000000000000000000000000b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1", // to address (padded)
	}
	data := "0x0000000000000000000000000000000000000000000000000de0b6b3a7640000" // 1 ETH in wei
	
	// Parse with ABI
	result, err := decoder.parseEventWithABI(topics, data, abiMethod)
	if err != nil {
		t.Fatalf("ABI parsing failed: %v", err)
	}
	
	// Verify parsed parameters
	if result["from"] != "0xa1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0" {
		t.Errorf("Expected from address 0xa1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0, got %s", result["from"])
	}
	
	if result["to"] != "0xb2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1" {
		t.Errorf("Expected to address 0xb2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1, got %s", result["to"])
	}
	
	// Should parse the value (1 ETH in wei = 1000000000000000000)
	if value, ok := result["value"].(uint64); ok {
		if value != 1000000000000000000 {
			t.Errorf("Expected value 1000000000000000000, got %d", value)
		}
	} else {
		// For large numbers, it might be returned as hex string
		if value, ok := result["value"].(string); ok {
			expected := "0x0000000000000000000000000000000000000000000000000de0b6b3a7640000"
			if value != expected {
				t.Errorf("Expected value %s, got %s", expected, value)
			}
		} else {
			t.Errorf("Expected value to be uint64 or string, got %T: %v", result["value"], result["value"])
		}
	}
}

func TestLogDecoder_RobustABIParsing_ERC1155TransferSingle(t *testing.T) {
	decoder := NewLogDecoder()
	
	// ERC1155 TransferSingle(address indexed operator, address indexed from, address indexed to, uint256 id, uint256 value)
	abiMethod := &ABIMethod{
		Name: "TransferSingle",
		Type: "event",
		Hash: "0xc3d58168c5ae7397731d063d5bbf3d657854427343f4c083240f7aacaa2d0f62",
		Inputs: []ABIInput{
			{Name: "operator", Type: "address", Indexed: true},
			{Name: "from", Type: "address", Indexed: true},
			{Name: "to", Type: "address", Indexed: true}, 
			{Name: "id", Type: "uint256", Indexed: false},
			{Name: "value", Type: "uint256", Indexed: false},
		},
	}
	
	topics := []string{
		"0xc3d58168c5ae7397731d063d5bbf3d657854427343f4c083240f7aacaa2d0f62", // TransferSingle signature
		"0x000000000000000000000000a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0", // operator (padded)
		"0x000000000000000000000000b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0", // from (padded)
		"0x000000000000000000000000c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1", // to (padded)
	}
	// Data contains id (0) and value (1) - each takes 32 bytes
	data := "0x0000000000000000000000000000000000000000000000000000000000000000" + // id = 0
		  "0000000000000000000000000000000000000000000000000000000000000001"   // value = 1
	
	result, err := decoder.parseEventWithABI(topics, data, abiMethod)
	if err != nil {
		t.Fatalf("ABI parsing failed: %v", err)
	}
	
	// Verify all parameters are correctly parsed
	expectedOperator := "0xa1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0"
	expectedFrom := "0xb1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0"
	expectedTo := "0xc2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1"
	
	if result["operator"] != expectedOperator {
		t.Errorf("Expected operator %s, got %s", expectedOperator, result["operator"])
	}
	if result["from"] != expectedFrom {
		t.Errorf("Expected from %s, got %s", expectedFrom, result["from"])
	}
	if result["to"] != expectedTo {
		t.Errorf("Expected to %s, got %s", expectedTo, result["to"])
	}
	
	// Check id and value from data
	if id, ok := result["id"].(uint64); ok {
		if id != 0 {
			t.Errorf("Expected id 0, got %d", id)
		}
	}
	if value, ok := result["value"].(uint64); ok {
		if value != 1 {
			t.Errorf("Expected value 1, got %d", value)
		}
	}
}

func TestLogDecoder_ABIFallbackToSignature(t *testing.T) {
	decoder := NewLogDecoder()
	
	// Test that when ABI parsing fails, it falls back to signature-based parsing
	baggage := map[string]interface{}{
		"raw_data": map[string]interface{}{
			"logs": []interface{}{
				map[string]interface{}{
					"address": "0xa0b86a33e6d93d5073cfa3e7b31fe6a6b93a2ed7",
					"topics": []interface{}{
						"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef", // Transfer
						"0x000000000000000000000000a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0", // from
						"0x000000000000000000000000b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1", // to
					},
					"data": "0x0000000000000000000000000000000000000000000000000de0b6b3a7640000",
				},
			},
		},
	}
	
	ctx := context.Background()
	err := decoder.Process(ctx, baggage)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}
	
	events, ok := baggage["events"].([]models.Event)
	if !ok {
		t.Fatalf("Events not found in baggage")
	}
	
	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}
	
	event := events[0]
	if event.Name != "Transfer" {
		t.Errorf("Expected event name Transfer, got %s", event.Name)
	}
	
	// Should have parsed parameters even without ABI
	if event.Parameters["from"] == nil || event.Parameters["to"] == nil {
		t.Errorf("Expected from/to parameters to be parsed")
	}
}

func TestLogDecoder_ABIParameterTypes(t *testing.T) {
	decoder := NewLogDecoder()
	
	// Test different parameter types
	testCases := []struct {
		input    ABIInput
		value    string
		expected interface{}
		isIndexed bool
	}{
		{
			input:     ABIInput{Name: "addr", Type: "address"},
			value:     "0x000000000000000000000000a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0",
			expected:  "0xa1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0",
			isIndexed: true,
		},
		{
			input:     ABIInput{Name: "flag", Type: "bool"},
			value:     "0x0000000000000000000000000000000000000000000000000000000000000001",
			expected:  true,
			isIndexed: false,
		},
		{
			input:     ABIInput{Name: "flag", Type: "bool"},
			value:     "0x0000000000000000000000000000000000000000000000000000000000000000",
			expected:  false,
			isIndexed: false,
		},
	}
	
	for _, tc := range testCases {
		result, err := decoder.parseABIParameter(tc.input, tc.value, tc.isIndexed)
		if err != nil {
			t.Errorf("Failed to parse %s: %v", tc.input.Type, err)
			continue
		}
		
		if result != tc.expected {
			t.Errorf("For type %s, expected %v, got %v", tc.input.Type, tc.expected, result)
		}
	}
} 