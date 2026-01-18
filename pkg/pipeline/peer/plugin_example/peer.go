package main

import (
	"encoding/json"
	"log"

	"github.com/edgeflare/pigo/pkg/pipeline"
	"github.com/edgeflare/pigo/pkg/pipeline/cdc"
)

type PeerExample struct{}

func (p *PeerExample) Pub(event cdc.Event, args ...any) error {
	log.Println("example connector plugin publish", event)
	return nil
}

func (p *PeerExample) Connect(config json.RawMessage, args ...any) error {
	log.Println("example connector plugin init", config)
	return nil
}

func (p *PeerExample) Sub(args ...any) (<-chan cdc.Event, error) {
	// for pub-only peers (sinks), or implement for sub/pubsub peers
	return nil, pipeline.ErrConnectorTypeMismatch
}

func (p *PeerExample) Type() pipeline.ConnectorType {
	return pipeline.ConnectorTypePub
}

func (p *PeerExample) Disconnect() error {
	return nil
}

var Connector pipeline.Connector = &PeerExample{}
