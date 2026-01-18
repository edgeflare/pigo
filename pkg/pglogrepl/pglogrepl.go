// Package pglogrepl provides Debezium-compatible change data capture (CDC) events from PostgreSQL write-ahead logs (WAL).
// It uses github.com/jackc/pglogrepl to read WAL, and then formats the changes into standardized CDC messages.
package pglogrepl

import (
	"cmp"
	"fmt"
	"time"
)

// Op represents a type of database operation to be replicated.
type Op string

const (
	OpInsert   Op = "insert"
	OpUpdate   Op = "update"
	OpDelete   Op = "delete"
	OpTruncate Op = "truncate"

	defaultStandbyUpdateInterval = 10 * time.Second
	defaultBufferSize            = 1000
	defaultPublication           = "pigo_pub"
	defaultSlot                  = "pigo_slot"
	defaultPlugin                = "pigoutput"
)

// Config holds replication configuration.
type Config struct {
	Publication string `json:"publication"`
	Slot        string `json:"slot"`
	Plugin      string `json:"plugin"`
	// Tables to add to publication. Example:
	// ["table_wo_schema", "specific_schema.example_table", "another_schema.*"]
	// ["*"] or ["*.*"] for all tables in all schemas
	Tables                []string      `json:"tables"`
	Ops                   []Op          `json:"ops"`
	PartitionRoot         bool          `json:"partitionRoot"`
	StandbyUpdateInterval time.Duration `json:"standbyUpdateInterval"`
	// ReplicaIdentity configures how much old row data is captured for each table.
	// not functional yet. manually execute sql to alter DEFAULT (streams primary key columns)
	ReplicaIdentity map[string]ReplicaIdentity `json:"relreplident"`
	BufferSize      int                        `json:"bufferSize"`
}

// ReplicaIdentity specifies what row data Postgres streams during UPDATE/DELETE operations:
//   - Default (d): streams primary key columns
//   - None (n): streams no old row data
//   - Full (f): streams all columns
//   - Index (i): streams columns in specified index
//
// Set with: ALTER TABLE table_name REPLICA IDENTITY [DEFAULT|NOTHING|FULL|USING INDEX name]
//
// Query with: SELECT relreplident FROM pg_class WHERE oid = 'schema.table'::regclass;
type ReplicaIdentity string

const (
	// ReplicaIdentityDefault streams only primary key columns
	ReplicaIdentityDefault ReplicaIdentity = "d"

	// ReplicaIdentityNothing streams no old row data
	ReplicaIdentityNothing ReplicaIdentity = "n"

	// ReplicaIdentityFull streams all columns of old rows
	ReplicaIdentityFull ReplicaIdentity = "f"

	// ReplicaIdentityIndex streams columns from a specified unique index
	ReplicaIdentityIndex ReplicaIdentity = "i"
)

func DefaultConfig() *Config {
	return &Config{
		Publication:           defaultPublication,
		Slot:                  defaultSlot,
		Plugin:                defaultPlugin,
		StandbyUpdateInterval: defaultStandbyUpdateInterval,
		Ops:                   []Op{OpInsert, OpUpdate, OpDelete, OpTruncate},
		BufferSize:            defaultBufferSize,
	}
}

func validateConfig(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config cannot be nil")
	}
	for _, op := range cfg.Ops {
		switch op {
		case OpInsert, OpUpdate, OpDelete, OpTruncate:
		default:
			return fmt.Errorf("invalid operation: %s", op)
		}
	}
	if cfg.StandbyUpdateInterval < time.Second {
		return fmt.Errorf("standby update interval must be at least 1 second")
	}
	return nil
}

func mergeWithDefaults(cfg *Config) *Config {
	def := DefaultConfig()
	if cfg == nil {
		return def
	}

	if len(cfg.Ops) == 0 {
		cfg.Ops = def.Ops
	}

	cfg.Publication = cmp.Or(cfg.Publication, def.Publication)
	cfg.Slot = cmp.Or(cfg.Slot, def.Slot)
	cfg.Plugin = cmp.Or(cfg.Plugin, def.Plugin)
	cfg.StandbyUpdateInterval = cmp.Or(cfg.StandbyUpdateInterval, def.StandbyUpdateInterval)
	cfg.BufferSize = cmp.Or(cfg.BufferSize, def.BufferSize)

	return cfg
}
