package transform

import (
	"testing"

	"github.com/edgeflare/pigo/internal/testutil"
	"github.com/edgeflare/pigo/pkg/pipeline/cdc"
)

func TestExtract(t *testing.T) {
	var cdc cdc.Event
	_, err := testutil.LoadJSON("cdc.json", &cdc)
	if err != nil {
		t.Fatalf("Failed to load JSON into struct: %v", err)
	}
	// t.Logf("Loaded CDC: %+v", cdc)

	testCases := []struct {
		want   map[string]any
		name   string
		fields []string
	}{
		{
			name:   "Extract multi fields of different types",
			fields: []string{"email", "id"},
			want: map[string]any{
				"email": "annek@noanswer.org",
				"id":    float64(1), // JSON numbers are decoded as float64
			},
		},
		{
			name:   "Extract only one field",
			fields: []string{"email"},
			want: map[string]any{
				"email": "annek@noanswer.org",
			},
		},
	}

	registry := NewRegistry()
	registry.Register("extract", func(config Config) Func {
		return Extract(config.(*ExtractConfig))
	})

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			extractConfig := &ExtractConfig{Fields: tc.fields}
			transform, err := registry.Get("extract")
			if err != nil {
				t.Fatalf("Failed to get transform function: %v", err)
			}

			transformedCDC, err := transform(extractConfig)(&cdc)
			if err != nil {
				t.Fatalf("Failed to apply transform: %v", err)
			}

			after, ok := transformedCDC.Payload.After.(map[string]interface{})
			if !ok {
				t.Fatalf("After payload is not in expected format")
			}

			// Verify all expected fields are present with correct values
			for field, expectedValue := range tc.want {
				value, exists := after[field]
				if !exists {
					t.Errorf("Field '%s' not found in the transformed CDC", field)
					continue
				}
				if value != expectedValue {
					t.Errorf("For field '%s': expected %v, got %v", field, expectedValue, value)
				}
			}

			// Verify only requested fields are present
			for field := range after {
				if _, expected := tc.want[field]; !expected {
					t.Errorf("Unexpected field '%s' found in the transformed CDC", field)
				}
			}
		})
	}
}
