package main

import (
	"log"
	"net/http"

	mw "github.com/edgeflare/pigo/pkg/httputil/middleware"
	"golang.org/x/net/webdav"
)

func main() {
	webdavHandler := &webdav.Handler{
		Prefix:     "/webdav/",
		FileSystem: webdav.Dir("./webdav-dir"),
		LockSystem: webdav.NewMemLS(),
		Logger: func(r *http.Request, err error) {
			if err != nil {
				log.Printf("WebDAV error: %s", err)
			}
		},
	}

	// Apply middlewares
	handler := mw.Add(webdavHandler,
		mw.RequestID,
		mw.CORSWithOptions(nil),
		mw.LoggerWithOptions(nil),
	)

	mux := http.NewServeMux()
	mux.Handle("/webdav/", handler)

	// Start the server using the ServeMux
	log.Println("Starting WebDAV server on :8080")
	http.ListenAndServe(":8080", mux)
}
