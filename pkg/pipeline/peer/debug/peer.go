package debug

import (
	"encoding/json"
	"log"

	"github.com/edgeflare/pigo/pkg/pipeline"
	"github.com/edgeflare/pigo/pkg/pipeline/cdc"
)

// PeerDebug is a debug peer that logs the data to the console
type PeerDebug struct{}

func (p *PeerDebug) Pub(event cdc.Event, _ ...any) error {
	// TODO: should take a log formatting arg
	log.Printf("%s %+v", pipeline.ConnectorDebug, event)
	return nil
}

func (p *PeerDebug) Connect(_ json.RawMessage, _ ...any) error {
	return nil
}

func (p *PeerDebug) Sub(_ ...any) (<-chan cdc.Event, error) {
	return nil, pipeline.ErrConnectorTypeMismatch
}

func (p *PeerDebug) Type() pipeline.ConnectorType {
	return pipeline.ConnectorTypePub
}

func (p *PeerDebug) Disconnect() error {
	return nil
}

func init() {
	pipeline.RegisterConnector(pipeline.ConnectorDebug, &PeerDebug{})
}
