package tools

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// BaggagePipeline orchestrates the execution of baggage processors in dependency order
type BaggagePipeline struct {
	processors map[string]Tool
	order      []string
	verbose    bool
}

// NewBaggagePipeline creates a new baggage pipeline
func NewBaggagePipeline() *BaggagePipeline {
	return &BaggagePipeline{
		processors: make(map[string]Tool),
		verbose:    true, // Default to verbose for better debugging
	}
}

// SetVerbose enables or disables verbose logging
func (p *BaggagePipeline) SetVerbose(verbose bool) {
	p.verbose = verbose
}

// AddProcessor adds a processor to the pipeline
func (p *BaggagePipeline) AddProcessor(processor Tool) error {
	name := processor.Name()
	if _, exists := p.processors[name]; exists {
		return fmt.Errorf("processor with name %s already exists", name)
	}

	p.processors[name] = processor

	// Set verbose mode on the processor if it supports it
	if verboseProcessor, ok := processor.(interface{ SetVerbose(bool) }); ok {
		verboseProcessor.SetVerbose(p.verbose)
	}

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

		if p.verbose {
			fmt.Printf("┌─ [%d/%d] %s\n", i+1, len(p.order), strings.ToUpper(name))
			fmt.Printf("│  🔧 %s\n", processor.Description())
		}

		stepStart := time.Now()
		err := processor.Process(ctx, baggage)
		stepDuration := time.Since(stepStart)

		if err != nil {
			if p.verbose {
				fmt.Printf("│  ❌ FAILED after %v: %v\n", stepDuration, err)
				fmt.Printf("└─ ❌ %s FAILED\n\n", strings.ToUpper(name))
			}
			return fmt.Errorf("processor %s failed: %w", name, err)
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
