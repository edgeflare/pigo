package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/edgeflare/pigo/pkg/httputil"
	mw "github.com/edgeflare/pigo/pkg/httputil/middleware"
	"github.com/edgeflare/pigo/pkg/pgx/schema"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Server struct {
	pool        *pgxpool.Pool
	mux         *http.ServeMux
	schemaCache *schema.Cache
	baseURL     string
	middleware  []httputil.Middleware
	httpServer  *http.Server
	omitempty   bool
}

func NewServer(connString, baseURL string, omitempty ...bool) (*Server, error) {
	schemaCache, err := schema.NewCache(connString)
	if err != nil {
		return nil, fmt.Errorf("failed to create schema cache: %w", err)
	}

	pool, err := pgxpool.New(context.Background(), connString)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	server := &Server{
		pool:        pool,
		mux:         http.NewServeMux(),
		schemaCache: schemaCache,
		baseURL:     baseURL,
		omitempty:   len(omitempty) > 0 && omitempty[0],
	}

	server.registerHandlers()
	server.registerRPCHandlers()
	server.addOpenAPIEndpoint()

	return server, nil
}

func (s *Server) AddMiddleware(middleware ...httputil.Middleware) {
	s.middleware = append(s.middleware, middleware...)
}

func (s *Server) registerHandlers() {
	s.mux.HandleFunc("/", s.wrapWithMiddleware(s.handleRequest))
}

func (s *Server) wrapWithMiddleware(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mw.Add(handler, s.middleware...).ServeHTTP(w, r)
	}
}

func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	// Get connection early and ensure it's always released
	_, conn, pgErr := httputil.ConnWithRole(r)
	if pgErr != nil {
		httputil.Error(w, http.StatusInternalServerError, pgErr.Error())
		return
	}
	defer conn.Release()

	// for metrics maybe later
	// startTime := time.Now()
	// defer func(rec *mw.ResponseRecorder) {
	// 	pgRole, ok := r.Context().Value(httputil.OIDCRoleClaimCtxKey).(string)
	// 	if !ok {
	// 		pgRole = "unknown"
	// 	}
	// 	log.Printf("%s %s %s %s %s %s %v", r.Method, r.URL.Path, r.RemoteAddr, time.Since(startTime), pgRole, r.UserAgent(), rec.StatusCode)
	// }(mw.NewResponseRecorder(w))

	path := strings.TrimPrefix(r.URL.Path, s.baseURL)
	if path == "" || path == "/" {
		html := fmt.Sprintf(`<!DOCTYPE html>
	<title>PIGO</title>
	<h1>PIGO REST API</h1>
	<h3>Auto-generated REST API for PostgreSQL</h3>
	<p><a href="%s/openapi.json">OpenAPI Specification</a></p>`, s.baseURL)

		httputil.HTML(w, http.StatusOK, html)
		return
	}

	tableSchema, err := s.tableFromPath(path)
	if err != nil {
		httputil.Error(w, http.StatusNotFound, err.Error())
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.handleGet(w, r, tableSchema)
	case http.MethodPost:
		s.handlePost(w, r, tableSchema)
	case http.MethodPatch:
		s.handlePatch(w, r, tableSchema)
	case http.MethodDelete:
		s.handleDelete(w, r, tableSchema)
	default:
		httputil.Error(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (s *Server) tableFromPath(path string) (schema.Table, error) {
	pathParts := strings.Split(strings.Trim(path, "/"), "/")
	if len(pathParts) < 1 {
		return schema.Table{}, fmt.Errorf("path should be /schema_name/table_name")
	}

	schemaName := "public"
	tableName := pathParts[0]
	if len(pathParts) > 1 {
		schemaName = pathParts[0]
		tableName = pathParts[1]
	}

	tableKey := fmt.Sprintf("%s.%s", schemaName, tableName)
	tableSchema, exists := s.schemaCache.Snapshot()[tableKey]
	if !exists {
		return schema.Table{}, fmt.Errorf("table %s not found or unauthorized", tableKey)
	}

	return tableSchema, nil
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request, table schema.Table) {
	params := parseQueryParams(r)

	query, args, err := buildSelectQuery(table, params)
	if err != nil {
		httputil.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	s.executeQuery(w, r, params, query, args)
}

func (s *Server) handlePost(w http.ResponseWriter, r *http.Request, table schema.Table) {
	var data map[string]any
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		httputil.Error(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	query, args, err := buildInsertQuery(table, data, parseHeaders(r))
	if err != nil {
		httputil.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	s.executeQuery(w, r, parseQueryParams(r), query, args)
}

func (s *Server) handlePatch(w http.ResponseWriter, r *http.Request, table schema.Table) {
	params := parseQueryParams(r)

	var data map[string]any
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		httputil.Error(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	query, args, err := buildUpdateQuery(table, data, params, parseHeaders(r))
	if err != nil {
		httputil.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	s.executeQuery(w, r, params, query, args)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request, table schema.Table) {
	params := parseQueryParams(r)

	query, args, err := buildDeleteQuery(table, params, parseHeaders(r))
	if err != nil {
		httputil.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	s.executeQuery(w, r, params, query, args)
}

func (s *Server) executeQuery(w http.ResponseWriter, r *http.Request, params QueryParams, query string, args []any) {
	_, conn, pgErr := httputil.ConnWithRole(r)
	if pgErr != nil {
		httputil.Error(w, http.StatusInternalServerError, pgErr.Error())
		return
	}

	pgRole, ok := r.Context().Value(httputil.OIDCRoleClaimCtxKey).(string)
	if !ok || pgRole == "" {
		log.Println("pgrole not found in OIDC claims")
		httputil.Error(w, http.StatusUnauthorized, "pgrole not found in OIDC claims")
		return
	}
	defer conn.Release()

	if parseHeaders(r).Prefer.WantsCountExact() {
		// construct count query using same logic but without LIMIT/OFFSET
		countParams := params
		countParams.Limit = 0
		countParams.Offset = 0

		table, err := s.tableFromPath(strings.TrimPrefix(r.URL.Path, s.baseURL))
		if err != nil {
			httputil.Error(w, http.StatusNotFound, err.Error())
			return
		}

		selectQueryWithoutLimitOffset, countArgs, err := buildSelectQuery(table, countParams)
		if err != nil {
			httputil.Error(w, http.StatusInternalServerError, fmt.Sprintf("count query build error: %s", err.Error()))
			return
		}

		countQuery := fmt.Sprintf("SELECT COUNT(*) FROM (%s) AS count_query", selectQueryWithoutLimitOffset)

		var rowCountExact int64
		err = conn.QueryRow(r.Context(), countQuery, countArgs...).Scan(&rowCountExact)
		if err != nil {
			httputil.Error(w, http.StatusInternalServerError, fmt.Sprintf("count error: %s", err.Error()))
			return
		}

		// set Content-Range header
		if params.Limit > 0 {
			start := int64(params.Offset) // defaults to 0 if no offset
			end := start + int64(params.Limit) - 1
			// ensure end doesn't exceed total count
			if end >= rowCountExact {
				end = rowCountExact - 1
			}

			// handle case where start is beyond total count
			if start >= rowCountExact {
				w.Header().Set("Content-Range", fmt.Sprintf("*/%d", rowCountExact))
			} else {
				w.Header().Set("Content-Range", fmt.Sprintf("%d-%d/%d", start, end, rowCountExact))
			}
		} else {
			w.Header().Set("Content-Range", fmt.Sprintf("*/%d", rowCountExact))
		}
	}

	rows, err := conn.Query(r.Context(), query, args...)
	if err != nil {
		log.Printf("TODO - map pg-err to http status: query error: %+v", err)
		httputil.Error(w, http.StatusInternalServerError, fmt.Sprintf("%s pgrole: %s", err.Error(), pgRole)) // debug
		return
	}
	defer rows.Close()

	var results []map[string]any
	if s.omitempty {
		results, err = collectRowsOmitNull(rows)
		if err != nil {
			log.Printf("TODO - map pg-err to http status: parse error: %v", err)
			httputil.Error(w, http.StatusInternalServerError, fmt.Sprintf("%s pgrole: %s", err.Error(), pgRole)) // debug
			return
		}
	} else {
		results, err = pgx.CollectRows(rows, pgx.RowToMap)
		if err != nil {
			log.Printf("TODO - map pg-err to http status: parse error: %v", err)
			httputil.Error(w, http.StatusInternalServerError, fmt.Sprintf("%s pgrole: %s", err.Error(), pgRole)) // debug
			return
		}
	}

	status := http.StatusOK
	if r.Method == http.MethodPost {
		status = http.StatusCreated
	}
	httputil.JSON(w, status, results)
}

func (s *Server) Start(addr string) error {
	if err := s.schemaCache.Init(context.Background()); err != nil {
		return fmt.Errorf("failed to initialize schema cache: %w", err)
	}

	go func() {
		for range s.schemaCache.Watch() {
			log.Println("reloaded schema cache")
		}
	}()

	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: s.mux,
	}

	log.Printf("Server starting on %s", addr)
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.schemaCache.Close()
	err := s.httpServer.Shutdown(ctx)
	s.pool.Close()
	return err
}

func (s *Server) addOpenAPIEndpoint() {
	info := schema.OpenAPIInfo{
		Title:       "PIGO REST API",
		Description: "Auto-generated REST API for PostgreSQL",
		Version:     "1.0.0",
	}
	info.Contact.Name = "edgeflare.io"
	info.Contact.Email = "support@edgeflare.io"

	openAPIHandler := func(w http.ResponseWriter, r *http.Request) {
		_, conn, pgErr := httputil.Conn(r)
		if pgErr != nil {
			httputil.Error(w, http.StatusInternalServerError, pgErr.Error())
			return
		}
		defer conn.Release()

		openAPIGenerator := schema.NewOpenAPIGenerator(s.schemaCache, s.baseURL, info)
		openAPIGenerator.ServeHTTP(w, r)
	}

	s.mux.Handle("/openapi.json", s.wrapWithMiddleware(openAPIHandler))
}
