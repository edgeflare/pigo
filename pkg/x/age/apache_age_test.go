package age_test

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	rest "github.com/edgeflare/pigo/pkg/x/age"
	"github.com/jackc/pgx/v5/pgxpool"
)

/*
// Example requests:

alias curlcmd='curl -X POST http://localhost:8080/graph -H "Content-Type: application/json"'

curlcmd -d '{"cypher": "CREATE (n:Person {name: \"Joe\", age: 65})"}'

curlcmd -d '{"cypher": "CREATE (n:Person {name: \"Jack\", age: 55})"}'

curlcmd -d '{"cypher": "CREATE (n:Person {name: \"Jane\", age: 45})"}'

curlcmd -d '{"cypher": "MATCH (a:Person {name: \"Joe\"}), (b:Person {name: \"Jack\"}) CREATE (a)-[r:KNOWS {since: 2020}]->(b)"}'

curlcmd -d '{"cypher": "MATCH (v) RETURN v", "columnCount": 1}'

curlcmd -d '{"cypher": "MATCH p=()-[r]-() RETURN p, r", "columnCount": 2}'
*/

func ExampleAGEHandler() {
	// Initialize a connection pool
	ctx := context.Background()
	graphName := "test_cypher"

	pool, err := pgxpool.New(ctx, cmp.Or(os.Getenv("DATABASE_URL"), "postgres://postgres:secret@localhost:5432/testdb"))
	if err != nil {
		log.Fatalf("failed to create connection pool: %v", err)
	}
	defer pool.Close()

	// Create an AGE handler with a default graph
	handler, err := rest.NewAGEHandler(ctx, pool, graphName)
	if err != nil {
		log.Fatalf("failed to create AGE handler: %v", err)
	}
	defer handler.Close()

	// Setup a test HTTP server
	server := httptest.NewServer(handler)
	defer server.Close()

	// // or mount on specific /path
	// mux := http.NewServeMux()
	// mux.Handle("POST /graph", handler)
	// if err = http.ListenAndServe(":8080", mux); err != nil {
	// 	log.Fatalf("Server error: %v", err)
	// }

	// Example 1: Create a vertex
	createReq := `{
		"cypher": "CREATE (n:Person {name: 'Alice', age: 30}) RETURN n", 
		"columnCount": 1
	}`

	resp, err := http.Post(server.URL, "application/json", strings.NewReader(createReq))
	if err != nil {
		log.Fatalf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	fmt.Printf("Create vertex status: %d\n", resp.StatusCode)

	// Example 2: Query vertices
	queryReq := `{
		"cypher": "MATCH (n:Person) RETURN n", 
		"columnCount": 1
	}`

	resp, err = http.Post(server.URL, "application/json", strings.NewReader(queryReq))
	if err != nil {
		log.Fatalf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	fmt.Printf("Query vertices status: %d\n", resp.StatusCode)

	// Output:
	// Create vertex status: 200
	// Query vertices status: 200
}
