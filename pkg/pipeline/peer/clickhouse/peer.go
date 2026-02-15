package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/edgeflare/pigo/pkg/pipeline"
	"github.com/edgeflare/pigo/pkg/pipeline/cdc"
	"github.com/edgeflare/pigo/pkg/util"
)

type PeerClickHouse struct {
	conn   driver.Conn
	config *clickhouse.Options
}

func (p *PeerClickHouse) Connect(config json.RawMessage, args ...any) error {
	p.config = &clickhouse.Options{}

	if config != nil {
		if err := json.Unmarshal(config, p.config); err != nil {
			return fmt.Errorf("failed to parse ClickHouse config: %w", err)
		}
	}

	// Set values from environment variables or use defaults
	if len(p.config.Addr) == 0 {
		p.config.Addr = []string{util.GetEnvOrDefault("PIGO_CLICKHOUSE_ADDR", "localhost:9000")}
	}
	if p.config.Auth.Database == "" {
		p.config.Auth.Database = util.GetEnvOrDefault("PIGO_CLICKHOUSE_AUTH_DATABASE", "default")
	}
	if p.config.Auth.Username == "" {
		p.config.Auth.Username = util.GetEnvOrDefault("PIGO_CLICKHOUSE_AUTH_USERNAME", "default")
	}
	if p.config.Auth.Password == "" {
		p.config.Auth.Password = util.GetEnvOrDefault("PIGO_CLICKHOUSE_AUTH_PASSWORD", "")
	}

	// Create a new ClickHouse connection
	conn, err := clickhouse.Open(p.config)
	if err != nil {
		return fmt.Errorf("failed to connect to ClickHouse: %w", err)
	}

	// Test the connection
	if err := conn.Ping(context.Background()); err != nil {
		return fmt.Errorf("failed to ping ClickHouse: %w", err)
	}

	p.conn = conn
	return nil
}

func (p *PeerClickHouse) Pub(event cdc.Event, args ...any) error {
	// TODO: FIX
	// sql := fmt.Sprintf(`
	// 	INSERT INTO %s.%s (
	// 		operation,
	// 		table_name,
	// 		schema_name,
	// 		timestamp,
	// 		data
	// 	) VALUES (?, ?, ?, ?, ?)
	// `, p.config.Auth.Database, event.Table)

	// // Convert the data to JSON
	// dataJSON, err := json.Marshal(event.Data)
	// if err != nil {
	// 	return fmt.Errorf("failed to marshal event data: %w", err)
	// }

	// // Execute the INSERT statement
	// err = p.conn.Exec(context.Background(), sql,
	// 	event.Operation,
	// 	event.Table,
	// 	event.Schema,
	// 	event.Timestamp,
	// 	dataJSON,
	// )
	// if err != nil {
	// 	return fmt.Errorf("failed to insert data into ClickHouse: %w", err)
	// }

	log.Printf("%v: Published event for table %v.%v", pipeline.ConnectorClickHouse, event.Schema, event.Payload.Source.Table)
	return nil
}

func (p *PeerClickHouse) Sub(args ...any) (<-chan cdc.Event, error) {
	// TODO: Implement Sub
	return nil, pipeline.ErrConnectorTypeMismatch
}

func (p *PeerClickHouse) Type() pipeline.ConnectorType {
	return pipeline.ConnectorTypePub
}

func (p *PeerClickHouse) Disconnect() error {
	if p.conn != nil {
		return p.conn.Close()
	}
	return nil
}

func init() {
	pipeline.RegisterConnector(pipeline.ConnectorClickHouse, &PeerClickHouse{})
}
