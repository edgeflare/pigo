package cdc

// Operation represents the type of change that occurred
type Operation string

const (
	OpCreate   Operation = "c"
	OpUpdate   Operation = "u"
	OpDelete   Operation = "d"
	OpRead     Operation = "r"
	OpTruncate Operation = "t"
)

// Source contains metadata about where a change originated
type Source struct {
	Version   string `json:"version"`
	Connector string `json:"connector"`
	Name      string `json:"name"`
	TsMs      int64  `json:"ts_ms"`
	Snapshot  bool   `json:"snapshot"`
	Db        string `json:"db"`
	Sequence  string `json:"sequence"`
	Schema    string `json:"schema"`
	Table     string `json:"table"`
	TxID      int64  `json:"txId"`
	Lsn       int64  `json:"lsn"`
	Xmin      *int64 `json:"xmin,omitempty"`
}

// Transaction contains metadata about the transaction this change belongs to
type Transaction struct {
	ID                  string `json:"id"`
	TotalOrder          int64  `json:"total_order"`
	DataCollectionOrder int64  `json:"data_collection_order"`
}

// Payload represents the actual change data
type Payload struct {
	Before      any          `json:"before"`
	After       any          `json:"after"`
	Source      Source       `json:"source"`
	Op          Operation    `json:"op"`
	TsMs        int64        `json:"ts_ms"`
	Transaction *Transaction `json:"transaction,omitempty"`
}

// Field represents a schema field definition
type Field struct {
	Field    string  `json:"field"`
	Type     string  `json:"type"`
	Optional bool    `json:"optional"`
	Name     string  `json:"name,omitempty"`
	Fields   []Field `json:"fields,omitempty"`
}

// Schema represents the schema definition for a change event
type Schema struct {
	Type     string  `json:"type"`
	Optional bool    `json:"optional"`
	Name     string  `json:"name"`
	Fields   []Field `json:"fields"`
}

// Event represents a complete change data capture event
type Event struct {
	Schema  Schema  `json:"schema"`
	Payload Payload `json:"payload"`
}

// SourceBuilder helps construct Source objects with reasonable defaults
type SourceBuilder struct {
	source Source
}

func NewSourceBuilder(connector, name string) *SourceBuilder {
	return &SourceBuilder{
		source: Source{
			Version:   "1.0",
			Connector: connector,
			Name:      name,
			Snapshot:  false,
			Sequence:  "[0,0]",
		},
	}
}

func (b *SourceBuilder) WithSchema(schema string) *SourceBuilder {
	b.source.Schema = schema
	return b
}

func (b *SourceBuilder) WithTable(table string) *SourceBuilder {
	b.source.Table = table
	return b
}

func (b *SourceBuilder) WithDatabase(db string) *SourceBuilder {
	b.source.Db = db
	return b
}

func (b *SourceBuilder) WithTimestamp(ts int64) *SourceBuilder {
	b.source.TsMs = ts
	return b
}

func (b *SourceBuilder) WithTransaction(txID int64, lsn int64) *SourceBuilder {
	b.source.TxID = txID
	b.source.Lsn = lsn
	return b
}

func (b *SourceBuilder) Build() Source {
	return b.source
}

// EventBuilder helps construct complete CDC events
type EventBuilder struct {
	event Event
}

func NewEventBuilder() *EventBuilder {
	return &EventBuilder{
		event: Event{
			Schema: Schema{
				Type:     "struct",
				Optional: false,
			},
		},
	}
}

func (b *EventBuilder) WithSource(source Source) *EventBuilder {
	b.event.Payload.Source = source
	return b
}

func (b *EventBuilder) WithOperation(op Operation) *EventBuilder {
	b.event.Payload.Op = op
	return b
}

func (b *EventBuilder) WithBefore(before any) *EventBuilder {
	b.event.Payload.Before = before
	return b
}

func (b *EventBuilder) WithAfter(after any) *EventBuilder {
	b.event.Payload.After = after
	return b
}

func (b *EventBuilder) WithTimestamp(ts int64) *EventBuilder {
	b.event.Payload.TsMs = ts
	return b
}

func (b *EventBuilder) WithTransaction(tx *Transaction) *EventBuilder {
	b.event.Payload.Transaction = tx
	return b
}

func (b *EventBuilder) Build() Event {
	return b.event
}
