package main

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/edgeflare/pigo/pkg/pglogrepl"
	"github.com/edgeflare/pigo/pkg/pipeline/cdc"
	"github.com/jackc/pgx/v5/pgconn"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// handle interrupt signals (e.g., Ctrl+C)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		cancel()
	}()

	conn, err := pgconn.Connect(context.Background(), cmp.Or(os.Getenv("PIGO_PGLOGREPL_CONN_STRING"),
		"postgres://postgres:secret@localhost:5432/testdb?replication=database"))
	if err != nil {
		log.Fatal("connect failed:", err)
	}
	defer conn.Close(context.Background())

	// configuration for logical replication. all options optional
	// cfg := pglogrepl.DefaultConfig()
	cfg := &pglogrepl.Config{
		Publication: "pigo_pub",
		Slot:        "pigo_slot",
		Plugin:      "pgoutput",
		// tables to watch/replicate
		Tables: []string{
			// "table_name",                // (usually public) default_schema.table_name
			// "schema_name.example_table", // specific schema.table
			// "schema_name.*",             // all tables in specified schema
			"*",
			// or "*.*" for all tables in all non-system schemas
		},
		Ops: []pglogrepl.Op{
			pglogrepl.OpInsert,
			pglogrepl.OpUpdate,
			pglogrepl.OpDelete,
		},
		StandbyUpdateInterval: 10 * time.Second,
		BufferSize:            1000, // go channel size
		// not functional yet. to capture old row data for UPDATE operations, manually execute
		// ALTER TABLE schema_name.table_name REPLICA IDENTITY FULL;
		// ReplicaIdentity: map[string]pglogrepl.ReplicaIdentity{"table_name": pglogrepl.ReplicaIdentityFull},
	}

	// start streaming changes
	events, err := pglogrepl.Stream(ctx, conn, cfg)
	if err != nil {
		log.Fatalf("Failed to start streaming: %v", err)
	}

	// process incoming CDC events
	for event := range events {
		switch event.Payload.Op {
		case cdc.OpCreate:
			fmt.Printf("Insert on %s: %v\n", event.Payload.Source.Table, event.Payload.After)
		case cdc.OpUpdate:
			fmt.Printf("Update on %s: Before=%v After=%v\n", event.Payload.Source.Table, event.Payload.Before, event.Payload.After)
		case cdc.OpDelete:
			fmt.Printf("Delete on %s: Before%v\n", event.Payload.Source.Table, event.Payload.Before)
		}
	}

	// check if the context was canceled
	if ctx.Err() != nil {
		log.Println("Streaming stopped:", ctx.Err())
	}
}
