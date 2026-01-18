package cdc

import (
	"testing"

	"github.com/edgeflare/pigo/internal/testutil"
)

// TestDebeziumConformanceCDC tests the conformance of the CDC struct to the Debezium CDC format.
func TestDebeziumConformanceCDC(t *testing.T) {
	var event Event
	_, err := testutil.LoadJSON("cdc.json", &event)
	if err != nil {
		t.Fatalf("Failed to load test data: %v", err)
	}
}
