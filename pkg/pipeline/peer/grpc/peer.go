package grpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/edgeflare/pigo/pkg/pipeline"
	"github.com/edgeflare/pigo/pkg/pipeline/cdc"
	pb "github.com/edgeflare/pigo/proto/generated"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// PeerGRPC implements both source and sink functionality for gRPC
type PeerGRPC struct {
	pipeline.Peer
	server *grpc.Server
	client pb.CDCStreamClient
	events chan cdc.Event
	conn   *grpc.ClientConn
	mu     sync.RWMutex
}

// streamServer implements the gRPC server for CDC streaming
type streamServer struct {
	pb.UnimplementedCDCStreamServer
	events chan cdc.Event
}

func (s *streamServer) Stream(_ *pb.StreamRequest, stream pb.CDCStream_StreamServer) error {
	for event := range s.events {
		if event.Payload.After == nil {
			continue
		}

		data, err := json.Marshal(event.Payload.After)
		if err != nil {
			continue
		}

		if err := stream.Send(&pb.CDCEvent{
			Table: event.Payload.Source.Schema + "." + event.Payload.Source.Table,
			Data:  data,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (p *PeerGRPC) Connect(config json.RawMessage, args ...any) error {
	var cfg struct {
		Address  string `json:"address"`  // e.g., "localhost:50051"
		IsServer bool   `json:"isServer"` // true for server mode, false for client mode
		TLS      struct {
			Enabled  bool   `json:"enabled"`
			CertFile string `json:"certFile"`
			KeyFile  string `json:"keyFile"`
			CAFile   string `json:"caFile"`
		} `json:"tls"`
	}

	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("failed to parse gRPC config: %w", err)
	}

	if cfg.Address == "" {
		return fmt.Errorf("gRPC address is required")
	}

	p.events = make(chan cdc.Event, 100)

	if cfg.IsServer {
		return p.startServer(cfg)
	}
	return p.connectClient(cfg)
}

func (p *PeerGRPC) startServer(cfg struct {
	Address  string `json:"address"`
	IsServer bool   `json:"isServer"`
	TLS      struct {
		Enabled  bool   `json:"enabled"`
		CertFile string `json:"certFile"`
		KeyFile  string `json:"keyFile"`
		CAFile   string `json:"caFile"`
	} `json:"tls"`
}) error {
	lis, err := net.Listen("tcp", cfg.Address)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	var opts []grpc.ServerOption
	if cfg.TLS.Enabled {
		// TODO: Implement TLS configuration
		return fmt.Errorf("TLS not yet implemented")
	}

	p.server = grpc.NewServer(opts...)
	pb.RegisterCDCStreamServer(p.server, &streamServer{events: p.events})

	go func() {
		if err := p.server.Serve(lis); err != nil {
			fmt.Printf("gRPC server error: %v\n", err)
		}
	}()

	return nil
}

func (p *PeerGRPC) Pub(event cdc.Event, args ...any) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.events != nil {
		p.events <- event
	}
	return nil
}

func (p *PeerGRPC) Sub(args ...any) (<-chan cdc.Event, error) {
	if p.client == nil {
		return nil, fmt.Errorf("not connected to gRPC server")
	}

	stream, err := p.client.Stream(context.Background(), &pb.StreamRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to create stream: %w", err)
	}

	events := make(chan cdc.Event, 100)
	go func() {
		defer close(events)
		for {
			event, err := stream.Recv()
			if err != nil {
				return
			}

			var data interface{}
			if err := json.Unmarshal(event.Data, &data); err != nil {
				continue
			}

			schemaTable := strings.Split(event.Table, ".")
			schema, table := "public", schemaTable[0]
			if len(schemaTable) == 2 {
				schema, table = schemaTable[0], schemaTable[1]
			}

			source := cdc.NewSourceBuilder("grpc", "grpc-source").
				WithDatabase("grpc").
				WithSchema(schema).
				WithTable(table).
				WithTimestamp(time.Now().UnixMilli()).
				Build()

			cdcEvent := cdc.NewEventBuilder().
				WithSource(source).
				WithOperation(cdc.OpCreate).
				WithAfter(data).
				WithTimestamp(time.Now().UnixMilli()).
				Build()

			events <- cdcEvent
		}
	}()

	return events, nil
}

func (p *PeerGRPC) Type() pipeline.ConnectorType {
	return pipeline.ConnectorTypePubSub
}

func (p *PeerGRPC) Disconnect() error {
	if p.server != nil {
		p.server.GracefulStop()
	}
	if p.conn != nil {
		p.conn.Close()
	}
	close(p.events)
	return nil
}

func (p *PeerGRPC) connectClient(cfg struct {
	Address  string `json:"address"`
	IsServer bool   `json:"isServer"`
	TLS      struct {
		Enabled  bool   `json:"enabled"`
		CertFile string `json:"certFile"`
		KeyFile  string `json:"keyFile"`
		CAFile   string `json:"caFile"`
	} `json:"tls"`
}) error {
	var opts []grpc.DialOption
	if !cfg.TLS.Enabled {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		// TODO: Implement TLS configuration
		return fmt.Errorf("TLS not yet implemented")
	}

	for retries := 0; retries < 3; retries++ {
		conn, err := grpc.NewClient(cfg.Address, opts...)
		if err == nil {
			p.conn = conn
			p.client = pb.NewCDCStreamClient(conn)
			return nil
		}
		time.Sleep(time.Second << uint(retries))
	}
	return fmt.Errorf("failed to connect after retries")
}

func init() {
	pipeline.RegisterConnector(pipeline.ConnectorGRPC, &PeerGRPC{})
}
