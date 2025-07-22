package tools

import (
	"context"

	"github.com/txplain/txplain/internal/models"
)

// AnnotationContextProvider is an interface that tools can implement to provide context for annotations
type AnnotationContextProvider interface {
	// GetAnnotationContext returns context items that can be used for generating annotations
	GetAnnotationContext(ctx context.Context, baggage map[string]interface{}) *models.AnnotationContext
}

// AnnotationContextCollector aggregates annotation context from multiple providers
type AnnotationContextCollector struct {
	providers []AnnotationContextProvider
}

// NewAnnotationContextCollector creates a new collector
func NewAnnotationContextCollector() *AnnotationContextCollector {
	return &AnnotationContextCollector{
		providers: make([]AnnotationContextProvider, 0),
	}
}

// AddProvider adds a context provider to the collector
func (acc *AnnotationContextCollector) AddProvider(provider AnnotationContextProvider) {
	acc.providers = append(acc.providers, provider)
}

// Collect gathers annotation context from all registered providers
func (acc *AnnotationContextCollector) Collect(ctx context.Context, baggage map[string]interface{}) *models.AnnotationContext {
	aggregated := &models.AnnotationContext{
		Items: make([]models.AnnotationContextItem, 0),
	}

	for _, provider := range acc.providers {
		if providerContext := provider.GetAnnotationContext(ctx, baggage); providerContext != nil {
			aggregated.Items = append(aggregated.Items, providerContext.Items...)
		}
	}

	return aggregated
} 