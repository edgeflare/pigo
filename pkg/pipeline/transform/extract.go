package transform

import (
	"fmt"

	"github.com/edgeflare/pigo/pkg/pipeline/cdc"
)

// ExtractConfig holds the configuration for the extract transformation
type ExtractConfig struct {
	Fields []string `json:"fields"`
}

// Validate validates the ExtractConfig
func (c *ExtractConfig) Validate() error {
	if len(c.Fields) == 0 {
		return fmt.Errorf("at least one field is required")
	}
	return nil
}

// Type returns the type of the transformation
func (c *ExtractConfig) Type() string {
	return "extract"
}

// Extract creates a Func that extracts specified fields from the CDC event
func Extract(config *ExtractConfig) Func {
	return func(cdc *cdc.Event) (*cdc.Event, error) {
		if err := config.Validate(); err != nil {
			return cdc, fmt.Errorf("invalid extract configuration: %w", err)
		}
		current := cdc
		fields := config.Fields
		newBefore := make(map[string]interface{})
		newAfter := make(map[string]interface{})

		if before, ok := current.Payload.Before.(map[string]interface{}); ok {
			for _, field := range fields {
				if value, exists := before[field]; exists {
					newBefore[field] = value
				}
			}
		}
		if after, ok := current.Payload.After.(map[string]interface{}); ok {
			for _, field := range fields {
				if value, exists := after[field]; exists {
					newAfter[field] = value
				}
			}
		}

		current.Payload.Before = newBefore
		current.Payload.After = newAfter

		return current, nil
	}
}
