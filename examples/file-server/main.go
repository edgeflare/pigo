package main

import (
	"embed"
	"flag"
	"fmt"
	"log"
	"net/http"

	mw "github.com/edgeflare/pigo/pkg/httputil/middleware"
)

// Embed the static directory
//
//go:embed dist/*
var embeddedFS embed.FS

var (
	port        = flag.Int("port", 8080, "port to listen on")
	directory   = flag.String("dir", "dist", "directory to serve files from")
	spaFallback = flag.Bool("spa", false, "fallback to index.html for not-found files")
	useEmbedded = flag.Bool("embed", false, "use embedded static files")
)

func main() {
	flag.Parse()

	mux := http.NewServeMux()

	var fs *embed.FS
	if *useEmbedded {
		fs = &embeddedFS
	}

	// other mux handlers
	//
	// static handler should be the last handler
	mux.Handle("GET /", mw.Static(*directory, *spaFallback, fs))

	// // or mount on a `/different/path` other than root `/`
	// mux.Handle("GET /static/", http.StripPrefix("/static", mw.Static(*directory, *spaFallback, fs)))

	// Start the server
	log.Printf("starting server on :%d", *port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *port), mux))
}

// go run ./examples/file-server/... -port 8081 -dir dist -spa -embed
// go run ./examples/file-server/... -port 8081 -dir examples/file-server/dist -spa
