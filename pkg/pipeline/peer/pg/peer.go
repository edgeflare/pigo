package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/edgeflare/pigo/pkg/pglogrepl"
	pg "github.com/edgeflare/pigo/pkg/pgx"
	"github.com/edgeflare/pigo/pkg/pgx/schema"
	"github.com/edgeflare/pigo/pkg/pipeline"
	"github.com/edgeflare/pigo/pkg/pipeline/cdc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errNotConnected = errors.New("not connected")

type PeerPG struct {
	pool        *pgxpool.Pool
	conn        *pgconn.PgConn
	cfg         Config
	schemaCache *schema.Cache
	tables      map[string]schema.Table
}

type Config struct {
	ConnString  string           `json:"connString"`
	Replication pglogrepl.Config `json:"replication"`
}

func (p *PeerPG) Connect(config json.RawMessage, _ ...any) error {
	if err := json.Unmarshal(config, &p.cfg); err != nil {
		return fmt.Errorf("config parse: %w", err)
	}

	connConfig, err := pgx.ParseConfig(p.cfg.ConnString)
	if err != nil {
		return fmt.Errorf("failed to parse connection string: %w", err)
	}

	// for replication connection ensure p.conn
	ctx := context.Background()
	if connConfig.RuntimeParams["replication"] == "database" {
		if p.conn, err = pgconn.Connect(ctx, p.cfg.ConnString); err != nil {
			return fmt.Errorf("failed to connect to PostgreSQL server: %w", err)
		}
		return nil
	}

	// otherwise ensure p.pool
	if p.pool, err = pgxpool.New(ctx, p.cfg.ConnString); err != nil {
		return err
	}

	if err = p.pool.Ping(ctx); err != nil {
		p.pool.Close()
		return fmt.Errorf("error connecting to database: %w", err)
	}

	if p.schemaCache, err = schema.NewCache(p.cfg.ConnString); err != nil {
		p.pool.Close()
		return fmt.Errorf("failed to create schema cache: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := p.schemaCache.Init(ctx); err != nil {
		p.pool.Close()
		p.schemaCache.Close()
		return fmt.Errorf("failed to initialize schema cache: %w", err)
	}

	go func() {
		for t := range p.schemaCache.Watch() {
			p.tables = t
		}
	}()
	return nil
}

func (p *PeerPG) Sub(_ ...any) (<-chan cdc.Event, error) {
	if p.conn == nil {
		return nil, errNotConnected
	}
	return pglogrepl.Stream(context.Background(), p.conn, &p.cfg.Replication)
}

func (p *PeerPG) Pub(event cdc.Event, _ ...any) error {
	if p.pool == nil || event.Payload.After == nil {
		return errNotConnected
	}

	tableName := event.Payload.Source.Table
	schemaName := event.Payload.Source.Schema
	fullTableName := fmt.Sprintf("%s.%s", schemaName, tableName)

	if tableName == "" {
		return fmt.Errorf("table name not found in CDC event")
	}

	table, exists := p.tables[fullTableName]
	if !exists {
		return fmt.Errorf("table %s not found in schema cache", fullTableName)
	}

	ctx := context.Background()
	switch event.Payload.Op {
	case cdc.OpCreate:
		return pg.InsertRow(ctx, p.pool, tableName, event.Payload.After, schemaName)
	case cdc.OpUpdate:
		where := map[string]any{}
		if event.Payload.Before == nil {
			return fmt.Errorf("before data missing for update operation")
		}

		for _, pkColumn := range table.PrimaryKeys {
			if val, ok := event.Payload.Before.(map[string]any)[pkColumn]; ok {
				where[pkColumn] = val
			}
		}
		if len(where) == 0 {
			return fmt.Errorf("no primary key values found in Before payload. To enable updates, execute 'ALTER TABLE %s REPLICA IDENTITY FULL;'", fullTableName)
		}
		return pg.UpdateRow(ctx, p.pool, tableName, event.Payload.After, where, schemaName)
	case cdc.OpDelete, cdc.OpTruncate:
		// TODO: implement delete
		return nil
	default:
		return fmt.Errorf("unknown operation")
	}
}

func (p *PeerPG) Type() pipeline.ConnectorType {
	return pipeline.ConnectorTypePubSub
}

func (p *PeerPG) Disconnect() error {
	if p.pool != nil {
		p.pool.Close()
	}
	if p.conn != nil {
		return p.conn.Close(context.Background())
	}
	return nil
}

func init() {
	pipeline.RegisterConnector(pipeline.ConnectorPostgres, &PeerPG{})
}
