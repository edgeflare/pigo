package pglogrepl

// import (
// 	"log"

// 	"github.com/edgeflare/pigo/pkg/pipeline/cdc"
// 	"github.com/jackc/pglogrepl"
// 	"github.com/jackc/pgx/v5/pgtype"
// )

// // processV1 processes a logical replication message (wal2json) from PostgreSQL WAL data.
// // Prefer pigoutput (which is default)
// // TODO: improve if deemed useful
// func processV1(walData []byte, relations map[uint32]*pglogrepl.RelationMessage, typeMap *pgtype.Map) ([]cdc.Event, error) {
// 	logicalMsg, err := pglogrepl.Parse(walData)
// 	if err != nil {
// 		log.Fatalf("Parse logical replication message: %s", err)
// 	}
// 	log.Printf("Receive a logical replication message: %s", logicalMsg.Type())
// 	switch logicalMsg := logicalMsg.(type) {
// 	case *pglogrepl.RelationMessage:
// 		relations[logicalMsg.RelationID] = logicalMsg

// 	case *pglogrepl.BeginMessage:
// 		// Indicates the beginning of a group of changes in a transaction. This is only sent for committed transactions.

// 	case *pglogrepl.CommitMessage:

// 	case *pglogrepl.InsertMessage:
// 		rel, ok := relations[logicalMsg.RelationID]
// 		if !ok {
// 			log.Fatalf("unknown relation ID %d", logicalMsg.RelationID)
// 		}
// 		values := map[string]interface{}{}
// 		for idx, col := range logicalMsg.Tuple.Columns {
// 			colName := rel.Columns[idx].Name
// 			switch col.DataType {
// 			case 'n': // null
// 				values[colName] = nil
// 			case 'u': // unchanged toast
// 				// This TOAST value was not changed. TOAST values are not stored in the tuple
// 			case 't': // text
// 				val, err := decodeTextColumnData(typeMap, col.Data, rel.Columns[idx].DataType)
// 				if err != nil {
// 					log.Fatalln("error decoding column data:", err)
// 				}
// 				values[colName] = val
// 			}
// 		}
// 		log.Printf("INSERT INTO %s.%s: %v", rel.Namespace, rel.RelationName, values)

// 	case *pglogrepl.UpdateMessage:
// 		// ...
// 	case *pglogrepl.DeleteMessage:
// 		// ...
// 	case *pglogrepl.TruncateMessage:
// 		// ...

// 	case *pglogrepl.TypeMessage:
// 	case *pglogrepl.OriginMessage:

// 	case *pglogrepl.LogicalDecodingMessage:
// 		log.Printf("Logical decoding message: %q, %q", logicalMsg.Prefix, logicalMsg.Content)

// 	case *pglogrepl.StreamStartMessageV2:
// 		log.Printf("Stream start message: xid %d, first segment? %d", logicalMsg.Xid, logicalMsg.FirstSegment)
// 	case *pglogrepl.StreamStopMessageV2:
// 		log.Printf("Stream stop message")
// 	case *pglogrepl.StreamCommitMessageV2:
// 		log.Printf("Stream commit message: xid %d", logicalMsg.Xid)
// 	case *pglogrepl.StreamAbortMessageV2:
// 		log.Printf("Stream abort message: xid %d", logicalMsg.Xid)
// 	default:
// 		log.Printf("Unknown message type in pigoutput stream: %T", logicalMsg)
// 	}
// 	return []cdc.Event{}, nil
// }
