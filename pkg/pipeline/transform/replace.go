package transform

import (
	"fmt"
	"regexp"

	"github.com/edgeflare/pigo/pkg/pipeline/cdc"
)

// ReplaceConfig holds the configuration for the replace transformation
type ReplaceConfig struct {
	// Schema replacements
	Schemas map[string]string `json:"schemas,omitempty"`

	// Table replacements
	Tables map[string]string `json:"tables,omitempty"`

	// Column/field replacements
	Columns map[string]string `json:"columns,omitempty"`

	// Regex replacements
	Regex []RegexReplacement `json:"regex,omitempty"`
}

// RegexReplacement defines a regex-based replacement rule
type RegexReplacement struct {
	Type    string `json:"type"`    // "schema", "table", or "column"
	Pattern string `json:"pattern"` // Regex pattern to match
	Replace string `json:"replace"` // Replacement string (can use regex groups)
}

// Validate validates the ReplaceConfig
func (c *ReplaceConfig) Validate() error {
	// Ensure at least one replacement type is configured
	if len(c.Schemas) == 0 &&
		len(c.Tables) == 0 &&
		len(c.Columns) == 0 &&
		len(c.Regex) == 0 {
		return fmt.Errorf("at least one replacement configuration is required")
	}

	// Validate regex patterns
	for _, regex := range c.Regex {
		if !isValidReplacementType(regex.Type) {
			return fmt.Errorf("invalid replacement type: %s", regex.Type)
		}
		if _, err := regexp.Compile(regex.Pattern); err != nil {
			return fmt.Errorf("invalid regex pattern %s: %w", regex.Pattern, err)
		}
	}

	return nil
}

func isValidReplacementType(t string) bool {
	return t == "schema" || t == "table" || t == "column"
}

// Type returns the type of the transformation
func (c *ReplaceConfig) Type() string {
	return "replace"
}

// Replace creates a Func that performs the configured replacements
func Replace(config *ReplaceConfig) Func {
	return func(cdc *cdc.Event) (*cdc.Event, error) {
		if err := config.Validate(); err != nil {
			return cdc, fmt.Errorf("invalid replace configuration: %w", err)
		}

		// Create a copy of the CDC event
		current := *cdc

		// Apply schema replacements
		if newSchema, exists := config.Schemas[current.Payload.Source.Schema]; exists {
			current.Payload.Source.Schema = newSchema
		}

		// Apply table replacements
		if newTable, exists := config.Tables[current.Payload.Source.Table]; exists {
			current.Payload.Source.Table = newTable
		}

		// Apply regex replacements
		for _, regex := range config.Regex {
			re := regexp.MustCompile(regex.Pattern)
			switch regex.Type {
			case "schema":
				current.Payload.Source.Schema = re.ReplaceAllString(current.Payload.Source.Schema, regex.Replace)
			case "table":
				current.Payload.Source.Table = re.ReplaceAllString(current.Payload.Source.Table, regex.Replace)
			}
		}

		// Apply column replacements to both Before and After payloads
		if len(config.Columns) > 0 {
			current.Payload.Before = replaceMapKeys(current.Payload.Before, config.Columns)
			current.Payload.After = replaceMapKeys(current.Payload.After, config.Columns)

			// Update schema fields if present
			for i, field := range current.Schema.Fields {
				if newName, exists := config.Columns[field.Field]; exists {
					current.Schema.Fields[i].Field = newName
					if field.Name != "" {
						current.Schema.Fields[i].Name = newName
					}
				}
			}
		}

		return &current, nil
	}
}

// replaceMapKeys creates a new map with replaced keys according to the replacements map
func replaceMapKeys(data interface{}, replacements map[string]string) interface{} {
	if data == nil {
		return nil
	}

	if m, ok := data.(map[string]interface{}); ok {
		newMap := make(map[string]interface{})
		for k, v := range m {
			newKey := k
			if replacement, exists := replacements[k]; exists {
				newKey = replacement
			}
			newMap[newKey] = v
		}
		return newMap
	}

	return data
}
