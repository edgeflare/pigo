package transform

import (
	"fmt"
	"sync"

	"github.com/edgeflare/pigo/pkg/pipeline/cdc"
	"github.com/mitchellh/mapstructure"
)

// Func is the signature for all transformation functions
type Func func(*cdc.Event) (*cdc.Event, error)

// Transformation represents a single transformation step (like Kafka SMT)
type Transformation struct {
	Config map[string]any `mapstructure:"config"`
	Type   string         `mapstructure:"type"`
}

// Config is the interface that all transformations must implement
type Config interface {
	// Validate validates the configuration
	Validate() error
	// Type returns the transformation type
	Type() string
}

// Registry is a collection of transformation functions
type Registry struct {
	transforms sync.Map // map[string]func(Config) Func
}

// Register adds a transformation to the registry
func (r *Registry) Register(name string, factory func(Config) Func) {
	r.transforms.Store(name, factory)
}

// Get returns a transformation from the registry
func (r *Registry) Get(name string) (func(Config) Func, error) {
	if value, ok := r.transforms.Load(name); ok {
		return value.(func(Config) Func), nil
	}
	return nil, fmt.Errorf("transformation %s not found", name)
}

// NewRegistry creates a new transformation registry
func NewRegistry() *Registry {
	return &Registry{
		transforms: sync.Map{},
	}
}

type Manager struct {
	registry *Registry
}

func NewManager() *Manager {
	return &Manager{
		registry: NewRegistry(),
	}
}

// RegisterBuiltins registers all built-in transformations
func (m *Manager) RegisterBuiltins() {
	m.registry.Register("extract", func(config Config) Func {
		if extractConfig, ok := config.(*ExtractConfig); ok {
			return Extract(extractConfig)
		}
		return func(cdc *cdc.Event) (*cdc.Event, error) {
			return cdc, fmt.Errorf("invalid config type for extract transformation")
		}
	})

	m.registry.Register("filter", func(config Config) Func {
		if filterConfig, ok := config.(*FilterConfig); ok {
			return Filter(filterConfig)
		}
		return func(cdc *cdc.Event) (*cdc.Event, error) {
			return cdc, fmt.Errorf("invalid config type for extract transformation")
		}
	})

	m.registry.Register("replace", func(config Config) Func {
		if replaceConfig, ok := config.(*ReplaceConfig); ok {
			return Replace(replaceConfig)
		}
		return func(cdc *cdc.Event) (*cdc.Event, error) {
			return cdc, fmt.Errorf("invalid config type for replace transformation")
		}
	})
}

// Chain creates a transformation chain from a list of configs
func (m *Manager) Chain(configs []Transformation) (Func, error) {
	var transforms []Func

	for _, cfg := range configs {
		factory, err := m.registry.Get(cfg.Type)
		if err != nil {
			return nil, fmt.Errorf("error getting transformation %s: %w", cfg.Type, err)
		}

		transformConfig, err := cfg.ToConfig()
		if err != nil {
			return nil, fmt.Errorf("error converting config for %s: %w", cfg.Type, err)
		}

		transform := factory(transformConfig)
		transforms = append(transforms, transform)
	}

	// Return a function that chains all transformations
	return func(cdc *cdc.Event) (*cdc.Event, error) {
		current := cdc
		var err error
		for _, t := range transforms {
			current, err = t(current)
			if err != nil {
				return nil, err
			}
			if current == nil {
				return nil, nil // Stop processing if any transformation returns nil
			}
		}
		return current, nil
	}, nil
}

// Helper method to convert Transformation to transform.Config interface
func (t *Transformation) ToConfig() (Config, error) {
	switch t.Type {
	case "extract":
		var cfg ExtractConfig
		if err := mapstructure.Decode(t.Config, &cfg); err != nil {
			return nil, fmt.Errorf("error decoding extract config: %w", err)
		}
		return &cfg, nil
	case "filter":
		var cfg FilterConfig
		if err := mapstructure.Decode(t.Config, &cfg); err != nil {
			return nil, fmt.Errorf("error decoding filter config: %w", err)
		}
		return &cfg, nil
	case "replace":
		var cfg ReplaceConfig
		if err := mapstructure.Decode(t.Config, &cfg); err != nil {
			return nil, fmt.Errorf("error decoding replace config: %w", err)
		}
		return &cfg, nil
	default:
		return nil, fmt.Errorf("unknown transformation type: %s", t.Type)
	}
}
