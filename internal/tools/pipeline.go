package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/txplain/txplain/internal/models"
)

// BaggagePipeline orchestrates the execution of baggage processors in dependency order
type BaggagePipeline struct {
	processors      map[string]Tool
	order           []string
	verbose         bool
	progressTracker *models.ProgressTracker
}

// NewBaggagePipeline creates a new baggage pipeline
func NewBaggagePipeline(verbose bool) *BaggagePipeline {
	return &BaggagePipeline{
		processors: make(map[string]Tool),
		verbose:    verbose,
	}
}

// NewBaggagePipelineWithProgress creates a new pipeline with progress tracking
func NewBaggagePipelineWithProgress(progressChan chan<- models.ProgressEvent, verbose bool) *BaggagePipeline {
	return &BaggagePipeline{
		processors:      make(map[string]Tool),
		verbose:         verbose,
		progressTracker: models.NewProgressTracker(progressChan),
	}
}

// AddProcessor adds a processor to the pipeline
func (p *BaggagePipeline) AddProcessor(processor Tool) error {
	name := processor.Name()
	if _, exists := p.processors[name]; exists {
		return fmt.Errorf("processor with name %s already exists", name)
	}

	p.processors[name] = processor

	// Recalculate execution order
	return p.calculateOrder()
}

// calculateOrder determines the execution order based on dependencies
func (p *BaggagePipeline) calculateOrder() error {
	// Use topological sort to determine execution order
	order, err := p.topologicalSort()
	if err != nil {
		return err
	}

	p.order = order
	return nil
}

// topologicalSort performs a topological sort on the processors based on dependencies
func (p *BaggagePipeline) topologicalSort() ([]string, error) {
	// Build adjacency list and in-degree count
	adjList := make(map[string][]string)
	inDegree := make(map[string]int)

	// Initialize all processors
	for name := range p.processors {
		adjList[name] = []string{}
		inDegree[name] = 0
	}

	// Build dependency graph
	for name, processor := range p.processors {
		deps := processor.Dependencies()
		for _, dep := range deps {
			if _, exists := p.processors[dep]; !exists {
				return nil, fmt.Errorf("processor %s depends on %s, but %s is not registered", name, dep, dep)
			}
			adjList[dep] = append(adjList[dep], name)
			inDegree[name]++
		}
	}

	// Kahn's algorithm for topological sorting
	var queue []string
	for name, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, name)
		}
	}

	var result []string
	for len(queue) > 0 {
		// Pop from queue
		current := queue[0]
		queue = queue[1:]
		result = append(result, current)

		// Process neighbors
		for _, neighbor := range adjList[current] {
			inDegree[neighbor]--
			if inDegree[neighbor] == 0 {
				queue = append(queue, neighbor)
			}
		}
	}

	// Check for circular dependencies
	if len(result) != len(p.processors) {
		return nil, fmt.Errorf("circular dependency detected in processor chain")
	}

	return result, nil
}

// Execute runs all processors in dependency order
func (p *BaggagePipeline) Execute(ctx context.Context, baggage map[string]interface{}) error {
	if len(p.order) == 0 {
		return fmt.Errorf("no processors registered or order not calculated")
	}

	// Add progress tracker to baggage so tools can use it
	if p.progressTracker != nil {
		baggage["progress_tracker"] = p.progressTracker
	}

	if p.verbose {
		fmt.Println("\n" + strings.Repeat("=", 80))
		fmt.Printf("🚀 STARTING TRANSACTION PROCESSING PIPELINE (%d tools)\n", len(p.order))
		fmt.Println(strings.Repeat("=", 80))

		// Show execution order
		fmt.Println("\n📋 EXECUTION ORDER:")
		for i, name := range p.order {
			processor := p.processors[name]
			deps := processor.Dependencies()
			if len(deps) == 0 {
				fmt.Printf("   %d. %s (no dependencies)\n", i+1, name)
			} else {
				fmt.Printf("   %d. %s (depends on: %v)\n", i+1, name, deps)
			}
		}
		fmt.Println()
	}

	startTime := time.Now()

	for i, name := range p.order {
		processor, exists := p.processors[name]
		if !exists {
			return fmt.Errorf("processor %s not found", name)
		}

		// Send progress update if progress tracker is available
		if p.progressTracker != nil {
			group := p.getToolGroup(name)
			title := p.getToolTitle(name)

			// Update component to initiated
			p.progressTracker.UpdateComponent(name, group, title, models.ComponentStatusInitiated, "Preparing to start...")

			// Update component to running
			p.progressTracker.UpdateComponent(name, group, title, models.ComponentStatusRunning, processor.Description())
		}

		if p.verbose {
			fmt.Printf("┌─ [%d/%d] %s\n", i+1, len(p.order), strings.ToUpper(name))
			fmt.Printf("│  🔧 %s\n", processor.Description())
		}

		stepStart := time.Now()
		err := processor.Process(ctx, baggage)
		stepDuration := time.Since(stepStart)

		if err != nil {
			// Update component to error if progress tracker is available
			if p.progressTracker != nil {
				group := p.getToolGroup(name)
				title := p.getToolTitle(name)
				p.progressTracker.UpdateComponent(name, group, title, models.ComponentStatusError, fmt.Sprintf("Failed: %v", err))
			}

			if p.verbose {
				fmt.Printf("│  ❌ FAILED after %v: %v\n", stepDuration, err)
				fmt.Printf("└─ ❌ %s FAILED\n\n", strings.ToUpper(name))
			}
			return fmt.Errorf("processor %s failed: %w", name, err)
		}

		// Update component to finished if progress tracker is available
		if p.progressTracker != nil {
			group := p.getToolGroup(name)
			title := p.getToolTitle(name)
			p.progressTracker.UpdateComponent(name, group, title, models.ComponentStatusFinished, fmt.Sprintf("Completed in %v", stepDuration))
		}

		if p.verbose {
			fmt.Printf("│  ✅ Completed in %v\n", stepDuration)
			fmt.Printf("└─ ✅ %s COMPLETED\n\n", strings.ToUpper(name))
		}
	}

	totalDuration := time.Since(startTime)

	if p.verbose {
		fmt.Println(strings.Repeat("=", 80))
		fmt.Printf("🎉 PIPELINE COMPLETED SUCCESSFULLY in %v\n", totalDuration)
		fmt.Printf("   Processed %d baggage items across %d tools\n", len(baggage), len(p.order))
		fmt.Println(strings.Repeat("=", 80) + "\n")
	}

	return nil
}

// GetExecutionOrder returns the current execution order
func (p *BaggagePipeline) GetExecutionOrder() []string {
	result := make([]string, len(p.order))
	copy(result, p.order)
	return result
}

// PrintExecutionOrder prints the execution order for debugging
func (p *BaggagePipeline) PrintExecutionOrder() {
	fmt.Println("Baggage Pipeline Execution Order:")
	for i, name := range p.order {
		processor := p.processors[name]
		deps := processor.Dependencies()
		if len(deps) == 0 {
			fmt.Printf("%d. %s (no dependencies)\n", i+1, name)
		} else {
			fmt.Printf("%d. %s (depends on: %v)\n", i+1, name, deps)
		}
	}
}

// ValidateAllDependencies checks that all dependencies are satisfied
func (p *BaggagePipeline) ValidateAllDependencies() error {
	for name, processor := range p.processors {
		deps := processor.Dependencies()
		for _, dep := range deps {
			if _, exists := p.processors[dep]; !exists {
				return fmt.Errorf("processor %s depends on %s, but %s is not registered", name, dep, dep)
			}
		}
	}
	return nil
}

// GetProcessorCount returns the number of registered processors
func (p *BaggagePipeline) GetProcessorCount() int {
	return len(p.processors)
}

// HasProcessor checks if a processor with the given name is registered
func (p *BaggagePipeline) HasProcessor(name string) bool {
	_, exists := p.processors[name]
	return exists
}

// GetProcessor returns a processor by name
func (p *BaggagePipeline) GetProcessor(name string) (Tool, bool) {
	processor, exists := p.processors[name]
	return processor, exists
}

// getToolGroup returns the appropriate group for a tool
func (p *BaggagePipeline) getToolGroup(toolName string) models.ComponentGroup {
	groupMap := map[string]models.ComponentGroup{
		// Data fetching phase
		"static_context_provider":      models.ComponentGroupData,
		"transaction_context_provider": models.ComponentGroupData,
		"abi_resolver":                 models.ComponentGroupData,

		// Decoding phase
		"trace_decoder":            models.ComponentGroupDecoding,
		"log_decoder":              models.ComponentGroupDecoding,
		"signature_resolver":       models.ComponentGroupDecoding,
		"token_transfer_extractor": models.ComponentGroupDecoding,

		// Enrichment phase
		"nft_decoder":             models.ComponentGroupEnrichment,
		"token_metadata_enricher": models.ComponentGroupEnrichment,
		"amounts_finder":          models.ComponentGroupEnrichment,
		"icon_resolver":           models.ComponentGroupEnrichment,
		"erc20_price_lookup":      models.ComponentGroupEnrichment,
		"monetary_value_enricher": models.ComponentGroupEnrichment,
		"ens_resolver":            models.ComponentGroupEnrichment,

		// Analysis phase
		"address_role_resolver": models.ComponentGroupAnalysis,
		"protocol_resolver":     models.ComponentGroupAnalysis,
		"tag_resolver":          models.ComponentGroupAnalysis,
		"transaction_explainer": models.ComponentGroupAnalysis,

		// Finishing phase
		"annotation_generator": models.ComponentGroupFinishing,
	}

	if group, exists := groupMap[toolName]; exists {
		return group
	}
	return models.ComponentGroupAnalysis // Default fallback
}

// getToolTitle returns a user-friendly title for a tool
func (p *BaggagePipeline) getToolTitle(toolName string) string {
	titleMap := map[string]string{
		"static_context_provider":      "Loading Protocol Database",
		"transaction_context_provider": "Processing Transaction Data",
		"abi_resolver":                 "Resolving Contract ABIs",
		"trace_decoder":                "Decoding Function Calls",
		"log_decoder":                  "Decoding Events",
		"signature_resolver":           "Resolving Method Signatures",
		"token_transfer_extractor":     "Extracting Token Transfers",
		"nft_decoder":                  "Processing NFT Data",
		"token_metadata_enricher":      "Fetching Token Metadata",
		"amounts_finder":               "Detecting Transaction Amounts",
		"icon_resolver":                "Loading Token Icons",
		"erc20_price_lookup":           "Fetching Token Prices",
		"monetary_value_enricher":      "Calculating USD Values",
		"ens_resolver":                 "Resolving ENS Names",
		"address_role_resolver":        "Analyzing Address Roles",
		"protocol_resolver":            "Identifying Protocols",
		"tag_resolver":                 "Generating Tags",
		"transaction_explainer":        "Generating AI Explanation",
		"annotation_generator":         "Creating Annotations",
	}

	if title, exists := titleMap[toolName]; exists {
		return title
	}
	return strings.Title(strings.ReplaceAll(toolName, "_", " ")) // Fallback
}
