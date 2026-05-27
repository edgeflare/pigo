package age

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/apache/age/drivers/golang/age"
	"github.com/edgeflare/pigo/pkg/httputil"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

// AGEHandler exposes AGE (Apache Graph Extension) graphs via REST
// WIP: only basic queries / functionalities have been tested
type AGEHandler struct {
	db    *sql.DB
	graph string
}

// AGERequest is the expected format for incoming API requests
type AGERequest struct {
	Cypher      string `json:"cypher"`
	ColumnCount int    `json:"columnCount,omitempty"` // for read requests. not required for write requests
}

// AGEResponse is the format for API responses
type AGEResponse struct {
	Success bool   `json:"success"`
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
}

// NewAGEHandler creates and configures a new API server
func NewAGEHandler(ctx context.Context, pool *pgxpool.Pool, graphName string) (*AGEHandler, error) {
	db := stdlib.OpenDBFromPool(pool)

	setupQueries := []string{
		"CREATE EXTENSION IF NOT EXISTS age;",
		"LOAD 'age';",
		"SET search_path = ag_catalog, \"$user\", public;",
	}

	for _, sq := range setupQueries {
		_, err := db.ExecContext(ctx, sq)
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("Failed to execute AGE setup command '%s': %w", sq, err)
		}
	}

	if _, err := age.GetReady(db, graphName); err != nil {
		db.Close()
		return nil, fmt.Errorf("Failed to initialize AGE with graph %s: %w", graphName, err)
	}

	return &AGEHandler{
		db:    db,
		graph: graphName,
	}, nil
}

// ServeHTTP processes Cypher queries
func (s *AGEHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req AGERequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.JSON(w, http.StatusBadRequest, AGEResponse{
			Success: false,
			Error:   "Invalid request body: " + err.Error(),
		})
		return
	}

	if req.Cypher == "" {
		httputil.JSON(w, http.StatusBadRequest, AGEResponse{
			Success: false,
			Error:   "Cypher query is required",
		})
		return
	}

	// Start a transaction
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		httputil.JSON(w, http.StatusInternalServerError, AGEResponse{
			Success: false,
			Error:   "Failed to begin transaction: " + err.Error(),
		})
		return
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	// Execute the Cypher query
	results, err := s.execCypher(tx, req)
	if err != nil {
		tx.Rollback()
		httputil.JSON(w, http.StatusInternalServerError, AGEResponse{
			Success: false,
			Error:   "Query execution Failed: " + err.Error(),
		})
		return
	}

	// Commit the transaction
	if err := tx.Commit(); err != nil {
		httputil.JSON(w, http.StatusInternalServerError, AGEResponse{
			Success: false,
			Error:   "Failed to commit transaction: " + err.Error(),
		})
		return
	}

	httputil.JSON(w, http.StatusOK, AGEResponse{
		Success: true,
		Data:    results,
	})
}

// execCypher wraps age.ExecCypher and returns the results and error, if any
func (s *AGEHandler) execCypher(tx *sql.Tx, req AGERequest) (any, error) {
	// If columnCount is 0, it's a write-only query
	if req.ColumnCount == 0 {
		_, err := age.ExecCypher(tx, s.graph, 0, "%s", req.Cypher)
		if err != nil {
			return nil, err
		}
		return map[string]string{"message": "Query executed successfully"}, nil
	}

	// Otherwise, it's a read query
	cursor, err := age.ExecCypher(tx, s.graph, req.ColumnCount, "%s", req.Cypher)
	if err != nil {
		return nil, err
	}
	defer cursor.Close()

	// Process results
	var results []map[string]any
	for cursor.Next() {
		row, err := cursor.GetRow()
		if err != nil {
			return nil, err
		}

		// Convert AGE entities to JSON-friendly structures
		rowMap := make(map[string]any)
		for i, entity := range row {
			rowMap[fmt.Sprintf("column%d", i)] = convertEntityToMap(entity)
		}
		results = append(results, rowMap)
	}

	return results, nil
}

// convertEntityToMap converts an AGE entity to a map for JSON marshaling
func convertEntityToMap(entity age.Entity) any {
	switch e := entity.(type) {
	case *age.Vertex:
		return map[string]any{
			"type":       "vertex",
			"id":         e.Id(),
			"label":      e.Label(),
			"properties": e.Props(),
		}
	case *age.Edge:
		return map[string]any{
			"type":       "edge",
			"id":         e.Id(),
			"label":      e.Label(),
			"startId":    e.StartId(),
			"endId":      e.EndId(),
			"properties": e.Props(),
		}
	case *age.Path:
		entities := make([]any, e.Size())
		for i := range e.Size() {
			entities[i] = convertEntityToMap(e.Get(i))
		}
		return map[string]any{
			"type":     "path",
			"entities": entities,
		}
	case *age.SimpleEntity:
		if e.IsNull() {
			return nil
		}
		return e.Value()
	default:
		return fmt.Sprintf("%v", entity)
	}
}

// Close performs cleanup when shutting down the server
func (s *AGEHandler) Close() {
	if s.db != nil {
		s.db.Close()
	}
}
