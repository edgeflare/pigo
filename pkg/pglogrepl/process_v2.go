package pglogrepl

import (
	"time"

	"github.com/edgeflare/pigo/pkg/pipeline/cdc"
	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"
)

func processV2(walData []byte, relations map[uint32]*pglogrepl.RelationMessageV2, typeMap *pgtype.Map, inStream *bool, dbName, dbHost string) []cdc.Event {
	logicalMsg, err := pglogrepl.ParseV2(walData, *inStream)
	if err != nil {
		zap.L().Fatal("ParseV2 failed", zap.Error(err))
	}

	var events []cdc.Event

	switch logicalMsg := logicalMsg.(type) {
	case *pglogrepl.RelationMessageV2:
		relations[logicalMsg.RelationID] = logicalMsg

	case *pglogrepl.InsertMessageV2:
		event := handleInsertMessageV2(logicalMsg, relations, typeMap, dbHost, dbName, int64(logicalMsg.Xid))
		events = append(events, event)

	case *pglogrepl.UpdateMessageV2:
		event := handleUpdateMessageV2(logicalMsg, relations, typeMap, dbHost, dbName, int64(logicalMsg.Xid))
		events = append(events, event)

	case *pglogrepl.DeleteMessageV2:
		event := handleDeleteMessageV2(logicalMsg, relations, typeMap, dbHost, dbName, int64(logicalMsg.Xid))
		events = append(events, event)

	case *pglogrepl.TruncateMessageV2:
		event := handleTruncateMessageV2(logicalMsg, relations, dbHost, dbName, int64(logicalMsg.Xid))
		events = append(events, event)

	case *pglogrepl.StreamStartMessageV2:
		*inStream = true

	case *pglogrepl.StreamStopMessageV2:
		*inStream = false
	}

	return events
}

func handleInsertMessageV2(msg *pglogrepl.InsertMessageV2, relations map[uint32]*pglogrepl.RelationMessageV2, typeMap *pgtype.Map, serverName, dbName string, lsn int64) cdc.Event {
	rel, ok := relations[msg.RelationID]
	if !ok {
		zap.L().Error("unknown relation ID", zap.Uint32("relationID", msg.RelationID))
		return cdc.Event{}
	}

	values := make(map[string]interface{})
	for idx, col := range msg.Tuple.Columns {
		colName := rel.Columns[idx].Name
		values[colName] = decodeColumn(col, typeMap, rel.Columns[idx].DataType)
	}

	source := cdc.NewSourceBuilder("postgresql", serverName).
		WithDatabase(dbName).
		WithSchema(rel.Namespace).
		WithTable(rel.RelationName).
		WithTransaction(int64(msg.Xid), lsn).
		WithTimestamp(time.Now().UnixMilli()).
		Build()

	return cdc.NewEventBuilder().
		WithSource(source).
		WithOperation(cdc.OpCreate).
		WithAfter(values).
		WithTimestamp(time.Now().UnixMilli()).
		Build()
}

func handleUpdateMessageV2(msg *pglogrepl.UpdateMessageV2, relations map[uint32]*pglogrepl.RelationMessageV2, typeMap *pgtype.Map, serverName, dbName string, lsn int64) cdc.Event {
	rel, ok := relations[msg.RelationID]
	if !ok {
		zap.L().Error("unknown relation ID", zap.Uint32("relationID", msg.RelationID))
		return cdc.Event{}
	}

	oldValues := make(map[string]interface{})
	if msg.OldTuple != nil {
		for idx, col := range msg.OldTuple.Columns {
			colName := rel.Columns[idx].Name
			oldValues[colName] = decodeColumn(col, typeMap, rel.Columns[idx].DataType)
		}
	}

	newValues := make(map[string]interface{})
	if msg.NewTuple != nil {
		for idx, col := range msg.NewTuple.Columns {
			colName := rel.Columns[idx].Name
			newValues[colName] = decodeColumn(col, typeMap, rel.Columns[idx].DataType)
		}
	}

	source := cdc.NewSourceBuilder("postgresql", serverName).
		WithDatabase(dbName).
		WithSchema(rel.Namespace).
		WithTable(rel.RelationName).
		WithTransaction(int64(msg.Xid), lsn).
		WithTimestamp(time.Now().UnixMilli()).
		Build()

	return cdc.NewEventBuilder().
		WithSource(source).
		WithOperation(cdc.OpUpdate).
		WithBefore(oldValues).
		WithAfter(newValues).
		WithTimestamp(time.Now().UnixMilli()).
		Build()
}

func handleDeleteMessageV2(msg *pglogrepl.DeleteMessageV2, relations map[uint32]*pglogrepl.RelationMessageV2, typeMap *pgtype.Map, serverName, dbName string, lsn int64) cdc.Event {
	rel, ok := relations[msg.RelationID]
	if !ok {
		zap.L().Error("unknown relation ID", zap.Uint32("relationID", msg.RelationID))
		return cdc.Event{}
	}

	oldValues := make(map[string]interface{})
	for idx, col := range msg.OldTuple.Columns {
		colName := rel.Columns[idx].Name
		oldValues[colName] = decodeColumn(col, typeMap, rel.Columns[idx].DataType)
	}

	source := cdc.NewSourceBuilder("postgresql", serverName).
		WithDatabase(dbName).
		WithSchema(rel.Namespace).
		WithTable(rel.RelationName).
		WithTransaction(int64(msg.Xid), lsn).
		WithTimestamp(time.Now().UnixMilli()).
		Build()

	return cdc.NewEventBuilder().
		WithSource(source).
		WithOperation(cdc.OpDelete).
		WithBefore(oldValues).
		WithTimestamp(time.Now().UnixMilli()).
		Build()
}

func handleTruncateMessageV2(msg *pglogrepl.TruncateMessageV2, relations map[uint32]*pglogrepl.RelationMessageV2, serverName, dbName string, lsn int64) cdc.Event {
	var rel *pglogrepl.RelationMessageV2
	for _, relation := range relations {
		rel = relation
		break
	}

	if rel == nil {
		zap.L().Error("no relations found for truncate message")
		return cdc.Event{}
	}

	source := cdc.NewSourceBuilder("postgresql", serverName).
		WithDatabase(dbName).
		WithSchema(rel.Namespace).
		WithTable(rel.RelationName).
		WithTransaction(int64(msg.Xid), lsn).
		WithTimestamp(time.Now().UnixMilli()).
		Build()

	return cdc.NewEventBuilder().
		WithSource(source).
		WithOperation(cdc.OpTruncate).
		WithTimestamp(time.Now().UnixMilli()).
		Build()
}
