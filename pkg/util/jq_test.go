package util

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/edgeflare/pigo/internal/testutil"
)

func TestJq(t *testing.T) {
	input, err := testutil.LoadJSON("k8s-svc.json")
	if err != nil {
		t.Fatalf("Failed to load test data: %v", err)
	}

	tests := []struct {
		expected any
		name     string
		path     string
		wantErr  bool
	}{
		// Basic path traversal
		{
			name:     "Get kind",
			path:     "kind",
			expected: "Service",
			wantErr:  false,
		},
		{
			name:     "Get apiVersion",
			path:     "apiVersion",
			expected: "v1",
			wantErr:  false,
		},

		// Nested object access
		{
			name:     "Get metadata name",
			path:     "metadata.name",
			expected: "nginx",
			wantErr:  false,
		},
		{
			name:     "Get deep nested label",
			path:     "metadata.labels.app",
			expected: "nginx",
			wantErr:  false,
		},

		// Array access with index
		{
			name:     "Get first port name",
			path:     "spec.ports[0].name",
			expected: "http",
			wantErr:  false,
		},
		{
			name:     "Get first port number",
			path:     "spec.ports[0].port",
			expected: float64(8080), // JSON numbers are parsed as float64
			wantErr:  false,
		},
		{
			name:     "Get second port name",
			path:     "spec.ports[1].name",
			expected: "https",
			wantErr:  false,
		},

		// Array wildcard access
		{
			name:     "Get all port names with []",
			path:     "spec.ports[].name",
			expected: []any{"http", "https"},
			wantErr:  false,
		},
		{
			name:     "Get all port names with [*]",
			path:     "spec.ports[*].name",
			expected: []any{"http", "https"},
			wantErr:  false,
		},
		{
			name:     "Get all port numbers",
			path:     "spec.ports[].port",
			expected: []any{float64(8080), float64(8443)},
			wantErr:  false,
		},

		// Empty/null object access
		{
			name:     "Get empty object",
			path:     "status.loadBalancer",
			expected: map[string]any{},
			wantErr:  false,
		},
		{
			name:     "Get null field",
			path:     "metadata.creationTimestamp",
			expected: nil,
			wantErr:  false,
		},

		// Error cases
		{
			name:    "Invalid array index",
			path:    "spec.ports[2].name",
			wantErr: true,
		},
		{
			name:    "Non-existent field",
			path:    "spec.nonexistent",
			wantErr: true,
		},
		{
			name:    "Invalid array syntax",
			path:    "spec.ports[abc].name",
			wantErr: true,
		},
		{
			name:    "Path to primitive as map",
			path:    "kind.subfield",
			wantErr: true,
		},
		{
			name:    "Empty path",
			path:    "",
			wantErr: true,
		},
		{
			name:    "Malformed array syntax",
			path:    "spec.ports[0.name",
			wantErr: true,
		},
		{
			name:    "Negative array index",
			path:    "spec.ports[-1].name",
			wantErr: true,
		},

		// Complex paths
		{
			name: "Get entire ports array",
			path: "spec.ports",
			expected: []any{
				map[string]any{
					"name":       "http",
					"protocol":   "TCP",
					"port":       float64(8080),
					"targetPort": float64(8080),
				},
				map[string]any{
					"name":       "https",
					"protocol":   "TCP",
					"port":       float64(8443),
					"targetPort": float64(8443),
				},
			},
			wantErr: false,
		},
		{
			name: "Get specific port object",
			path: "spec.ports[0]",
			expected: map[string]any{
				"name":       "http",
				"protocol":   "TCP",
				"port":       float64(8080),
				"targetPort": float64(8080),
			},
			wantErr: false,
		},
		{
			name:     "Get service type",
			path:     "spec.type",
			expected: "ClusterIP",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Jq(input, tt.path)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Jq() expected error for path %s but got none", tt.path)
				}
				return
			}

			if err != nil {
				t.Errorf("Jq() unexpected error for path %s: %v", tt.path, err)
				return
			}

			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("Jq() for path %s = %v, want %v", tt.path, got, tt.expected)
			}
		})
	}

	// Test with nil input
	t.Run("Nil input", func(t *testing.T) {
		_, err := Jq(nil, "any.path")
		if err == nil {
			t.Error("Jq() expected error for nil input but got none")
		}
	})
}

func BenchmarkJq(b *testing.B) {
	// Parse the test JSON once for all benchmarks
	input, err := testutil.LoadJSON("k8s-svc.json")
	if err != nil {
		b.Fatalf("Failed to load CDC: %v", err)
	}

	// Define benchmark cases for different access patterns
	benchmarks := []struct {
		name string
		path string
	}{
		// Simple key access
		{
			name: "SimpleKey",
			path: "kind",
		},
		// Nested key access
		{
			name: "NestedKey",
			path: "metadata.name",
		},
		// Deep nested key access
		{
			name: "DeepNestedKey",
			path: "metadata.labels.app",
		},
		// Array index access
		{
			name: "ArrayIndex",
			path: "spec.ports[0].name",
		},
		// Array wildcard access
		{
			name: "ArrayWildcard",
			path: "spec.ports[].name",
		},
		// Array star wildcard access
		{
			name: "ArrayStarWildcard",
			path: "spec.ports[*].name",
		},
		// Full object access
		{
			name: "FullObject",
			path: "spec.ports[0]",
		},
		// Empty object access
		{
			name: "EmptyObject",
			path: "status.loadBalancer",
		},
		// Null value access
		{
			name: "NullValue",
			path: "metadata.creationTimestamp",
		},
		// Full array access
		{
			name: "FullArray",
			path: "spec.ports",
		},
	}

	// Run benchmarks
	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			b.ResetTimer() // Reset timer before each benchmark
			for b.Loop() {
				_, err := Jq(input, bm.path)
				if err != nil {
					b.Fatalf("Unexpected error: %v", err)
				}
			}
		})
	}
}

// BenchmarkJqErrors benchmarks error cases
func BenchmarkJqErrors(b *testing.B) {
	input, err := testutil.LoadJSON("k8s-svc.json")
	if err != nil {
		b.Fatalf("Failed to load CDC: %v", err)
	}

	errorCases := []struct {
		name string
		path string
	}{
		{
			name: "NonExistentKey",
			path: "nonexistent",
		},
		{
			name: "InvalidArrayIndex",
			path: "spec.ports[99].name",
		},
		{
			name: "MalformedArraySyntax",
			path: "spec.ports[abc].name",
		},
		{
			name: "EmptyPath",
			path: "",
		},
		{
			name: "PrimitiveAsMap",
			path: "kind.subfield",
		},
	}

	for _, ec := range errorCases {
		b.Run("Error_"+ec.name, func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = Jq(input, ec.path)
			}
		})
	}
}

// BenchmarkJqWithDifferentSizes tests performance with varying input sizes
func BenchmarkJqWithDifferentSizes(b *testing.B) {
	// Create test data with different sizes
	createLargeInput := func(size int) map[string]any {
		ports := make([]any, size)
		for i := 0; i < size; i++ {
			ports[i] = map[string]any{
				"name":       fmt.Sprintf("port-%d", i),
				"protocol":   "TCP",
				"port":       8080 + i,
				"targetPort": 8080 + i,
			}
		}
		return map[string]any{
			"kind":       "Service",
			"apiVersion": "v1",
			"spec": map[string]any{
				"ports": ports,
			},
		}
	}

	sizes := []int{1, 10, 100, 1000}
	paths := []string{
		"spec.ports[0].name", // First element
		"spec.ports[].name",  // Wildcard
		"spec.ports[*].name", // Star wildcard
	}

	for _, size := range sizes {
		input := createLargeInput(size)
		for _, path := range paths {
			b.Run(fmt.Sprintf("Size_%d_Path_%s", size, path), func(b *testing.B) {
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					_, err := Jq(input, path)
					if err != nil {
						b.Fatalf("Unexpected error: %v", err)
					}
				}
			})
		}
	}
}
