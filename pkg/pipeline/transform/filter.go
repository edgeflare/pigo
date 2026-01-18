package transform

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/edgeflare/pigo/pkg/pipeline/cdc"
)

type FilterConfig struct {
	TablePattern  string   `json:"tablePattern,omitempty"`
	Tables        []string `json:"tables,omitempty"`
	ExcludeTables []string `json:"excludeTables,omitempty"`
	Operations    []string `json:"operations,omitempty"`
}

func (c *FilterConfig) Validate() error {
	if len(c.Tables) == 0 && len(c.ExcludeTables) == 0 &&
		c.TablePattern == "" && len(c.Operations) == 0 {
		return fmt.Errorf("at least one filter criteria required")
	}

	if c.TablePattern != "" {
		if _, err := regexp.Compile(c.TablePattern); err != nil {
			return fmt.Errorf("invalid table pattern: %w", err)
		}
	}

	validOps := map[string]bool{"c": true, "u": true, "d": true, "r": true}
	for _, op := range c.Operations {
		if !validOps[op] {
			return fmt.Errorf("invalid operation: %s", op)
		}
	}

	return nil
}

func Filter(config *FilterConfig) Func {
	if err := config.Validate(); err != nil {
		return func(cdc *cdc.Event) (*cdc.Event, error) {
			return nil, fmt.Errorf("invalid filter configuration: %w", err)
		}
	}

	var tableRegex *regexp.Regexp
	if config.TablePattern != "" {
		tableRegex = regexp.MustCompile(config.TablePattern)
	}

	var includeRefs []tableRef
	var excludeRefs []tableRef
	for _, table := range config.Tables {
		includeRefs = append(includeRefs, parseTableRef(table))
	}
	for _, table := range config.ExcludeTables {
		excludeRefs = append(excludeRefs, parseTableRef(table))
	}

	return func(cdc *cdc.Event) (*cdc.Event, error) {
		// Validate CDC event structure
		if cdc == nil || cdc.Payload.Source.Schema == "" || cdc.Payload.Source.Table == "" {
			return nil, fmt.Errorf("invalid CDC event: missing schema or table")
		}

		// Filter by operations if specified
		if len(config.Operations) > 0 {
			if cdc.Payload.Op == "" {
				return nil, fmt.Errorf("invalid CDC event: missing operation")
			}

			opMatched := false
			for _, op := range config.Operations {
				if op == string(cdc.Payload.Op) {
					opMatched = true
					break
				}
			}
			if !opMatched {
				// nil, nil cause nil-pointer derefence
				return nil, nil // Skip event if operation does not match
			}
		}

		// Filter by excluded tables
		for _, ref := range excludeRefs {
			if matchesTableRef(cdc, ref) {
				return nil, nil
			}
		}

		// Filter by included tables
		if len(includeRefs) > 0 {
			included := false
			for _, ref := range includeRefs {
				if matchesTableRef(cdc, ref) {
					included = true
					break
				}
			}
			if !included {
				return nil, nil
			}
		}

		// Filter by table pattern
		if tableRegex != nil {
			tableFullName := fmt.Sprintf("%s.%s", cdc.Payload.Source.Schema, cdc.Payload.Source.Table)
			if !tableRegex.MatchString(tableFullName) && !tableRegex.MatchString(cdc.Payload.Source.Table) {
				return nil, nil
			}
		}

		return cdc, nil
	}
}

type tableRef struct {
	schema string
	table  string
	isGlob bool
}

func parseTableRef(ref string) tableRef {
	// Handle special case of *.*
	if ref == "*.*" {
		return tableRef{schema: "*", table: "*", isGlob: true}
	}

	parts := strings.Split(ref, ".")
	if len(parts) == 2 {
		return tableRef{
			schema: parts[0],
			table:  parts[1],
			isGlob: strings.Contains(parts[0], "*") || strings.Contains(parts[1], "*"),
		}
	}
	return tableRef{
		table:  ref,
		isGlob: strings.Contains(ref, "*"),
	}
}

func (c *FilterConfig) Type() string {
	return "filter"
}

// matchesTableRef checks if a CDC event matches a table reference
func matchesTableRef(cdc *cdc.Event, ref tableRef) bool {
	if ref.isGlob {
		// Handle global wildcard *.* case
		if ref.schema == "*" && ref.table == "*" {
			return true
		}

		// Handle schema.* case
		if ref.schema != "*" && ref.table == "*" {
			matched, _ := filepath.Match(ref.schema, cdc.Payload.Source.Schema)
			return matched
		}

		// Handle *.table case
		if ref.schema == "*" && ref.table != "*" {
			matched, _ := filepath.Match(ref.table, cdc.Payload.Source.Table)
			return matched
		}

		// Handle patterns in both schema and table
		schemaMatched, _ := filepath.Match(ref.schema, cdc.Payload.Source.Schema)
		tableMatched, _ := filepath.Match(ref.table, cdc.Payload.Source.Table)
		return schemaMatched && tableMatched
	}

	// Non-glob exact matching
	if ref.schema != "" && ref.schema != cdc.Payload.Source.Schema {
		return false
	}
	return ref.table == cdc.Payload.Source.Table
}
