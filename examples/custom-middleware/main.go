package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/edgeflare/pigo/pkg/httputil"
)

func main() {
	r := httputil.NewRouter()

	// custom logger middleware
	r.Use(customLogger)

	// Group with prefix "/api/v1"
	v1 := r.Group("/api/v1")

	// Handle routes in the "/api" group
	v1.Handle("GET /users/{user}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(fmt.Sprintf("User endpoint: %s", r.PathValue("user"))))
	}))
	v1.Handle("POST /products/{product}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(fmt.Sprintf("Product endpoint: %s", r.PathValue("product"))))
	}))

	log.Fatal(r.ListenAndServe(":8080"))
}

func customLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Println(r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
