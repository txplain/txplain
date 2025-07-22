package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/txplain/txplain/internal/models"
)

// Test data for ERC721 and ERC1155 transfers
func TestNFTDecoder_ERC721_Transfer(t *testing.T) {
	decoder := NewNFTDecoder()
	
	// Simulate ERC721 Transfer event from a real transaction
	events := []models.Event{
		{
			Name:     "Transfer",
			Contract: "0xbc4ca0eda7647a8ab7c2061c2e118a18a936f13d", // BoredApes contract
			Parameters: map[string]interface{}{
				"from":    "0x000000000000000000000000a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0",
				"to":      "0x000000000000000000000000b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1",
				"tokenId": "0x0000000000000000000000000000000000000000000000000000000000000c9a", // 3226 in hex
			},
		},
	}
	
	ctx := context.Background()
	nftTransfers := decoder.extractNFTTransfers(ctx, events)
	
	if len(nftTransfers) != 1 {
		t.Fatalf("Expected 1 NFT transfer, got %d", len(nftTransfers))
	}
	
	transfer := nftTransfers[0]
	if transfer.Type != "ERC721" {
		t.Errorf("Expected type ERC721, got %s", transfer.Type)
	}
	if transfer.TokenID != "3226" {
		t.Errorf("Expected token ID 3226, got %s", transfer.TokenID)
	}
	if transfer.Amount != "1" {
		t.Errorf("Expected amount 1, got %s", transfer.Amount)
	}
}

func TestNFTDecoder_ERC1155_TransferSingle(t *testing.T) {
	decoder := NewNFTDecoder()
	
	// Simulate ERC1155 TransferSingle event
	events := []models.Event{
		{
			Name:     "TransferSingle",
			Contract: "0x495f947276749ce646f68ac8c248420045cb7b5e", // OpenSea shared contract
			Parameters: map[string]interface{}{
				"from":  "0x000000000000000000000000a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0",
				"to":    "0x000000000000000000000000b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1",
				"id":    "0x0000000000000000000000000000000000000000000000000000000000000c9a", // 3226 in hex
				"value": "0x0000000000000000000000000000000000000000000000000000000000000002", // 2 in hex
			},
		},
	}
	
	ctx := context.Background()
	nftTransfers := decoder.extractNFTTransfers(ctx, events)
	
	if len(nftTransfers) != 1 {
		t.Fatalf("Expected 1 NFT transfer, got %d", len(nftTransfers))
	}
	
	transfer := nftTransfers[0]
	if transfer.Type != "ERC1155" {
		t.Errorf("Expected type ERC1155, got %s", transfer.Type)
	}
	if transfer.TokenID != "3226" {
		t.Errorf("Expected token ID 3226, got %s", transfer.TokenID)
	}
	if transfer.Amount != "2" {
		t.Errorf("Expected amount 2, got %s", transfer.Amount)
	}
}

func TestNFTDecoder_ERC1155_WithFixedLogDecoder(t *testing.T) {
	decoder := NewNFTDecoder()
	
	// Simulate properly decoded ERC1155 TransferSingle event with fixed log decoder
	events := []models.Event{
		{
			Name:     "TransferSingle",
			Contract: "0xae5da858cbab6cebfba1ce138dc68b310f638f68",
			Parameters: map[string]interface{}{
				"operator": "0x000000000000000000000000a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0",
				"from":     "0x000000000000000000000000b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0",
				"to":       "0x000000000000000000000000c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1", 
				"id":       "0x0000000000000000000000000000000000000000000000000000000000000000", // 0 in hex
				"value":    "0x0000000000000000000000000000000000000000000000000000000000000001", // 1 in hex
			},
		},
	}
	
	ctx := context.Background()
	nftTransfers := decoder.extractNFTTransfers(ctx, events)
	
	if len(nftTransfers) != 1 {
		t.Fatalf("Expected 1 NFT transfer, got %d", len(nftTransfers))
	}
	
	transfer := nftTransfers[0]
	if transfer.Type != "ERC1155" {
		t.Errorf("Expected type ERC1155, got %s", transfer.Type)
	}
	if transfer.TokenID != "0" {
		t.Errorf("Expected token ID 0, got %s", transfer.TokenID)
	}
	if transfer.Amount != "1" {
		t.Errorf("Expected amount 1, got %s", transfer.Amount)
	}
	
	// Most importantly, check that From/To addresses are properly extracted and cleaned
	expectedFrom := "0xb1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0"
	expectedTo := "0xc2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1"
	
	if transfer.From != expectedFrom {
		t.Errorf("Expected From address %s, got %s", expectedFrom, transfer.From)
	}
	if transfer.To != expectedTo {
		t.Errorf("Expected To address %s, got %s", expectedTo, transfer.To)
	}
}

func TestNFTDecoder_AddressCleanup(t *testing.T) {
	decoder := NewNFTDecoder()
	
	// Test address cleanup with padded addresses
	tests := []struct {
		input    string
		expected string
	}{
		{"0x000000000000000000000000a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0", "0xa1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0"},
		{"0xa1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0", "0xa1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0"},
		{"", ""},
	}
	
	for _, test := range tests {
		result := decoder.cleanAddress(test.input)
		if result != test.expected {
			t.Errorf("cleanAddress(%s) = %s, expected %s", test.input, result, test.expected)
		}
	}
}

func TestNFTDecoder_TokenIDCleanup(t *testing.T) {
	decoder := NewNFTDecoder()
	
	// Test token ID cleanup
	tests := []struct {
		input    string
		expected string
	}{
		{"0x0000000000000000000000000000000000000000000000000000000000000c9a", "3226"},
		{"0x01", "1"},
		{"0x00", "0"},
		{"", "0"},
		{"1234", "1234"},
	}
	
	for _, test := range tests {
		result := decoder.cleanTokenID(test.input)
		if result != test.expected {
			t.Errorf("cleanTokenID(%s) = %s, expected %s", test.input, result, test.expected)
		}
	}
}

func TestNFTDecoder_Process_Integration(t *testing.T) {
	// Skip if no environment variables are set for integration testing
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	
	decoder := NewNFTDecoder()
	ctx := context.Background()
	
	// Create baggage with mixed ERC721 and ERC1155 events
	baggage := map[string]interface{}{
		"events": []models.Event{
			{
				Name:     "Transfer", // ERC721
				Contract: "0xbc4ca0eda7647a8ab7c2061c2e118a18a936f13d",
				Parameters: map[string]interface{}{
					"from":    "0x000000000000000000000000a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0",
					"to":      "0x000000000000000000000000b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1",
					"tokenId": "0x0000000000000000000000000000000000000000000000000000000000000001", // ERC721 token ID 1
				},
			},
			{
				Name:     "TransferSingle", // ERC1155
				Contract: "0x495f947276749ce646f68ac8c248420045cb7b5e",
				Parameters: map[string]interface{}{
					"from":  "0x000000000000000000000000a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0",
					"to":    "0x000000000000000000000000b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1",
					"id":    "0x0000000000000000000000000000000000000000000000000000000000000c9a",
					"value": "0x0000000000000000000000000000000000000000000000000000000000000002",
				},
			},
			{
				Name:     "Transfer", // ERC20 (should be ignored)
				Contract: "0xa0b86a33e6d93d5073cfa3e7b31fe6a6b93a2ed7",
				Parameters: map[string]interface{}{
					"from":  "0x000000000000000000000000a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0",
					"to":    "0x000000000000000000000000b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1",
					"value": "0x0000000000000000000000000000000000000000000000000de0b6b3a7640000", // No tokenId
				},
			},
		},
	}
	
	err := decoder.Process(ctx, baggage)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}
	
	// Check results
	nftTransfers, ok := baggage["nft_transfers"].([]NFTTransfer)
	if !ok {
		t.Fatalf("nft_transfers not found in baggage")
	}
	
	if len(nftTransfers) != 2 {
		t.Fatalf("Expected 2 NFT transfers, got %d", len(nftTransfers))
	}
	
	// Verify ERC721 transfer
	erc721Found := false
	erc1155Found := false
	
	for _, transfer := range nftTransfers {
		if transfer.Type == "ERC721" {
			erc721Found = true
			if transfer.TokenID != "1" {
				t.Errorf("ERC721 token ID should be 1, got %s", transfer.TokenID)
			}
			if transfer.Amount != "1" {
				t.Errorf("ERC721 amount should be 1, got %s", transfer.Amount)
			}
		} else if transfer.Type == "ERC1155" {
			erc1155Found = true
			if transfer.TokenID != "3226" {
				t.Errorf("ERC1155 token ID should be 3226, got %s", transfer.TokenID)
			}
			if transfer.Amount != "2" {
				t.Errorf("ERC1155 amount should be 2, got %s", transfer.Amount)
			}
		}
	}
	
	if !erc721Found {
		t.Error("ERC721 transfer not found")
	}
	if !erc1155Found {
		t.Error("ERC1155 transfer not found")
	}
	
	// Verify debug info was added
	debugInfo, ok := baggage["debug_info"].(map[string]interface{})
	if !ok {
		t.Fatalf("debug_info not found in baggage")
	}
	
	nftDebug, ok := debugInfo["nft_decoder"].([]string)
	if !ok {
		t.Fatalf("nft_decoder debug info not found")
	}
	
	if len(nftDebug) == 0 {
		t.Error("No debug info recorded")
	}
	
	t.Logf("Debug info: %v", nftDebug)
}

func TestNFTDecoder_GetPromptContext(t *testing.T) {
	decoder := NewNFTDecoder()
	ctx := context.Background()
	
	// Create baggage with NFT transfers
	nftTransfers := []NFTTransfer{
		{
			Type:           "ERC721",
			Contract:       "0xbc4ca0eda7647a8ab7c2061c2e118a18a936f13d",
			From:           "0xa1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0",
			To:             "0xb2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1",
			TokenID:        "1234",
			Amount:         "1",
			CollectionName: "Bored Ape Yacht Club",
		},
		{
			Type:           "ERC1155",
			Contract:       "0x495f947276749ce646f68ac8c248420045cb7b5e",
			From:           "0xa1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0",
			To:             "0xb2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1",
			TokenID:        "3226",
			Amount:         "2",
			CollectionName: "Tappers Kingdom",
		},
	}
	
	baggage := map[string]interface{}{
		"nft_transfers": nftTransfers,
	}
	
	context := decoder.GetPromptContext(ctx, baggage)
	
	if context == "" {
		t.Fatalf("Expected non-empty context, got empty string")
	}
	
	// Check that context includes expected information
	expectedStrings := []string{
		"### NFT Transfers:",
		"#### ERC-721 Tokens Transferred: 1",
		"#### ERC-1155 Tokens Transferred: 1",
		"Bored Ape Yacht Club",
		"Tappers Kingdom",
		"Token ID: 1234",
		"Token ID: 3226",
		"Amount: 2",
	}
	
	for _, expected := range expectedStrings {
		if !strings.Contains(context, expected) {
			t.Errorf("Context missing expected string: %s\nContext: %s", expected, context)
		}
	}
	
	t.Logf("Generated context:\n%s", context)
} 