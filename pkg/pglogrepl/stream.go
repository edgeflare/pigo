package pglogrepl

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/edgeflare/pigo/pkg/pipeline/cdc"
	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
)

// Stream starts logical replication and returns a channel of CDC events.
func Stream(ctx context.Context, conn *pgconn.PgConn, cfg *Config) (<-chan cdc.Event, error) {
	if conn == nil {
		return nil, fmt.Errorf("nil connection")
	}

	cfg = mergeWithDefaults(cfg)
	if err := validateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	events := make(chan cdc.Event, cfg.BufferSize)
	if err := setupReplication(ctx, conn, cfg); err != nil {
		close(events)
		return nil, fmt.Errorf("setup replication: %w", err)
	}

	go streamEvents(ctx, conn, cfg, events)
	return events, nil
}

func setupReplication(ctx context.Context, conn *pgconn.PgConn, cfg *Config) error {
	if err := ensurePublication(ctx, conn, *cfg); err != nil {
		return fmt.Errorf("publication: %w", err)
	}

	sysID, err := pglogrepl.IdentifySystem(ctx, conn)
	if err != nil {
		return fmt.Errorf("identify system: %w", err)
	}

	if err := ensureSlot(conn, cfg.Slot, cfg.Plugin); err != nil {
		return fmt.Errorf("slot: %w", err)
	}

	pluginArgs := []string{
		"proto_version '4'",
		fmt.Sprintf("publication_names '%s'", cfg.Publication),
		"messages 'true'",
		"streaming 'true'",
	}

	return pglogrepl.StartReplication(ctx, conn, cfg.Slot, sysID.XLogPos, pglogrepl.StartReplicationOptions{
		PluginArgs: pluginArgs,
	})
}

func ensureSlot(conn *pgconn.PgConn, name, plugin string) error {
	exists, err := checkExists(conn, "pg_replication_slots", "slot_name", name)
	if err != nil {
		return err
	}

	if !exists {
		_, err = pglogrepl.CreateReplicationSlot(context.Background(), conn, name, plugin,
			pglogrepl.CreateReplicationSlotOptions{Temporary: false})
	}
	return err
}

func streamEvents(ctx context.Context, conn *pgconn.PgConn, cfg *Config, events chan<- cdc.Event) {
	defer close(events)
	relations := make(map[uint32]*pglogrepl.RelationMessageV2)
	typeMap := newTypeMap() // pgtype.NewMap()
	nextStandby := time.Now().Add(cfg.StandbyUpdateInterval)
	var walPos pglogrepl.LSN
	inStream := false
	serverAddr := conn.Conn().RemoteAddr().String()

	for {
		if time.Now().After(nextStandby) {
			if err := pglogrepl.SendStandbyStatusUpdate(ctx, conn, pglogrepl.StandbyStatusUpdate{WALWritePosition: walPos}); err != nil {
				return
			}
			nextStandby = time.Now().Add(cfg.StandbyUpdateInterval)
		}

		msgCtx, cancel := context.WithDeadline(ctx, nextStandby)
		msg, err := conn.ReceiveMessage(msgCtx)
		cancel()

		if err != nil {
			if !pgconn.Timeout(err) {
				return
			}
			continue
		}

		copyData, ok := msg.(*pgproto3.CopyData)
		if !ok {
			continue
		}

		switch copyData.Data[0] {
		case pglogrepl.PrimaryKeepaliveMessageByteID:
			pkm, err := pglogrepl.ParsePrimaryKeepaliveMessage(copyData.Data[1:])
			if err != nil {
				continue
			}
			if pkm.ServerWALEnd > walPos {
				walPos = pkm.ServerWALEnd
			}

		case pglogrepl.XLogDataByteID:
			xld, err := pglogrepl.ParseXLogData(copyData.Data[1:])
			if err != nil {
				continue
			}
			if xld.WALStart > walPos {
				walPos = xld.WALStart
			}
			for _, event := range processV2(xld.WALData, relations, typeMap, &inStream, "", serverAddr) {
				events <- event
			}
		}
	}
}

func ensurePublication(ctx context.Context, conn *pgconn.PgConn, cfg Config) error {
	exists, err := checkExists(conn, "pg_publication", "pubname", cfg.Publication)
	if err != nil {
		return err
	}

	if !exists {
		var createStmt strings.Builder
		fmt.Fprintf(&createStmt, "CREATE PUBLICATION %s", cfg.Publication)

		pubObj := parsePublicationTables(cfg.Tables)

		if pubObj.allTables == true {
			createStmt.WriteString(" FOR ALL TABLES")
		} else if len(pubObj.schemas) > 0 {
			fmt.Fprintf(&createStmt, " FOR TABLES IN SCHEMA %s", strings.Join(pubObj.schemas, ", "))
		} else if len(pubObj.tables) > 0 {
			fmt.Fprintf(&createStmt, " FOR TABLE %s", strings.Join(pubObj.tables, ", "))
		}

		var params []string
		if len(cfg.Ops) > 0 {
			ops := make([]string, len(cfg.Ops))
			for i, o := range cfg.Ops {
				ops[i] = string(o)
			}
			params = append(params, fmt.Sprintf("publish = '%s'", strings.Join(ops, ", ")))
		}
		if cfg.PartitionRoot {
			params = append(params, "publish_via_partition_root = true")
		}
		if cfg.PublishGeneratedColumns != PublishGeneratedColumnsUnset {
			verNum, err := serverVersionNum(ctx, conn)
			if err != nil {
				return fmt.Errorf("checking server version for publish_generated_columns: %w", err)
			}
			if verNum < minPgVersionGeneratedCols {
				return fmt.Errorf("publishGeneratedColumns=%q requires PostgreSQL 18+, server is %d",
					cfg.PublishGeneratedColumns, verNum)
			}
			params = append(params, fmt.Sprintf("publish_generated_columns = '%s'", cfg.PublishGeneratedColumns))
		}

		if len(params) > 0 {
			createStmt.WriteString(" WITH (")
			createStmt.WriteString(strings.Join(params, ", "))
			createStmt.WriteString(")")
		}

		if _, err := conn.Exec(ctx, createStmt.String()).ReadAll(); err != nil {
			return fmt.Errorf("create publication: %w", err)
		}
	} else {
		if err := reconcilePublication(ctx, conn, cfg); err != nil {
			return fmt.Errorf("alter publication: %w", err)
		}
	}

	return nil
}

func checkExists(conn *pgconn.PgConn, table, column, value string) (bool, error) {
	if table != "pg_publication" && table != "pg_replication_slots" {
		return false, fmt.Errorf("invalid table name")
	}
	if column != "pubname" && column != "slot_name" {
		return false, fmt.Errorf("invalid column name")
	}

	rows, err := conn.Exec(context.Background(),
		fmt.Sprintf("SELECT EXISTS (SELECT 1 FROM %s WHERE %s = '%s')", table, column, value)).ReadAll()
	if err != nil {
		return false, fmt.Errorf("check exists: %w", err)
	}
	return len(rows) > 0 && len(rows[0].Rows) > 0 && string(rows[0].Rows[0][0]) == "t", nil
}

type tablePattern struct {
	allTables bool     // true if *.* or * is specified
	schemas   []string // schema names for schema.* patterns
	tables    []string // specific table names
}

func parsePublicationTables(patterns []string) tablePattern {
	var tp tablePattern

	for _, p := range patterns {
		if p == "*" || p == "*.*" {
			return tablePattern{allTables: true}
		}

		if idx := strings.LastIndex(p, ".*"); idx > 0 {
			tp.schemas = append(tp.schemas, p[:idx])
			continue
		}

		tp.tables = append(tp.tables, p)
	}

	return tp
}

func reconcilePublication(ctx context.Context, conn *pgconn.PgConn, cfg Config) error {
	var params []string
	if cfg.PublishGeneratedColumns != PublishGeneratedColumnsUnset {
		verNum, err := serverVersionNum(ctx, conn)
		if err != nil {
			return fmt.Errorf("checking server version for publish_generated_columns: %w", err)
		}
		if verNum < minPgVersionGeneratedCols {
			return fmt.Errorf("publishGeneratedColumns=%q requires PostgreSQL 18+, server is %d",
				cfg.PublishGeneratedColumns, verNum)
		}
		params = append(params, fmt.Sprintf("publish_generated_columns = '%s'", cfg.PublishGeneratedColumns))
	}
	if len(params) == 0 {
		return nil
	}
	sql := fmt.Sprintf("ALTER PUBLICATION %s SET (%s)", cfg.Publication, strings.Join(params, ", "))
	if _, err := conn.Exec(ctx, sql).ReadAll(); err != nil {
		return fmt.Errorf("alter publication: %w", err)
	}
	return nil
}
