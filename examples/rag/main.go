package main

// See [docs/rag.md](../../docs/rag.md) for more information.

import (
	"context"
	"fmt"
	"log"

	"github.com/edgeflare/pigo/pkg/util"
	"github.com/edgeflare/pigo/pkg/x/rag"
	"github.com/jackc/pgx/v5"
)

func main() {
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, util.GetEnvOrDefault("PGO_POSTGRES_CONN_STRING", "postgres://postgres:secret@localhost:5432/postgres"))
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer conn.Close(ctx)

	// Create a new RAG client
	client, err := rag.NewClient(conn, rag.DefaultConfig())
	client.Config.TableName = "lms.courses"
	// TODO: fix primary key data type from primary key column in contentSelectQuery

	if err != nil {
		log.Fatalf("Failed to create RAG client: %v", err)
	}

	err = client.CreateEmbedding(ctx, "SELECT id, CONCAT('title:', title, ', summary:', summary) AS content FROM lms.courses")
	// err = client.CreateEmbedding(ctx, "") // CreateEmbedding constructs content by concatenating colname:value of other columns for each row
	// err = client.CreateEmbedding(ctx) // Assumes the table has a column named `content` that contains the content for which embedding will be created
	if err != nil {
		log.Fatalf("Failed to create embeddings: %v", err)
	}

	fmt.Println("Embeddings have been successfully created.")

	// retrieval example
	input := "example input text"
	limit := 2

	results, err := client.Retrieve(ctx, input, limit)
	if err != nil {
		log.Fatalf("Failed to retrieve content: %v", err)
	}

	// Print the retrieved results
	for _, r := range results {
		fmt.Printf("ID: %v\nContent: %s\nEmbedding: %v\n", r.PK, r.Content, r.Embedding.Slice()[0])
	}

	prompt := "count the courses. just give me the number."
	// this is requesting infromation from internal data
	// typically LLM models don't have access to your data
	// unless you exposed publicly for models to access and be trained on
	// give it a shot with Generate function. it will likely spit out gibberish ie hallucinate
	response, err := client.Generate(ctx, prompt)
	if err != nil {
		log.Fatalf("Failed to generate content: %v", err)
	}
	fmt.Println(string(response))

	// Now augment the prompt with the retrieved data
	// number of retrieved rows (relevant contents) from embeddings table to augment the prompt with
	retrievalLimit := 3
	// this is the query to retrieve the relevant contents from the embeddings table
	// retrievalInput is optional. if not provided, the prompt is used as the input to retrieve the relevant contents from the embeddings table
	retrievalInput := "python, machine learning, data science"
	responseWithRetrieval, err := client.GenerateWithRetrieval(ctx, prompt, retrievalLimit, retrievalInput)
	if err != nil {
		log.Fatalf("Failed to generate content: %v", err)
	}
	fmt.Println(string(responseWithRetrieval))
}
