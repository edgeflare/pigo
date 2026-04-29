package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/edgeflare/pigo/pkg/httputil"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// FunctionParam represents a PostgreSQL function parameter
type FunctionParam struct {
	Name         string
	DataType     string
	DefaultValue *string
	Mode         string // IN, OUT, INOUT, VARIADIC
	Position     int
}

// Function represents a PostgreSQL function
type Function struct {
	Schema      string
	Name        string
	Parameters  []FunctionParam
	ReturnType  string
	IsSetof     bool // Returns a set of records
	Language    string
	IsAggregate bool
}

// Add RPC handler registration to Server
func (s *Server) registerRPCHandlers() {
	s.mux.HandleFunc(strings.TrimRight(s.basePath, "/")+"/rpc/", s.wrapWithMiddleware(s.handleRPC))
}

// handleRPC handles function calls via /rpc/function_name
func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	// Get connection early and ensure it's always released
	_, conn, pgErr := httputil.ConnWithRole(r)
	if pgErr != nil {
		httputil.Error(w, http.StatusInternalServerError, pgErr.Error())
		return
	}
	defer conn.Release()

	path := strings.TrimPrefix(r.URL.Path, s.basePath+"/rpc/")
	if path == "" {
		httputil.Error(w, http.StatusBadRequest, "Function name required")
		return
	}

	// Parse schema.function or just function
	pathParts := strings.Split(strings.Trim(path, "/"), "/")
	fmt.Println(pathParts, len(pathParts))
	if len(pathParts) < 1 {
		httputil.Error(w, http.StatusBadRequest, "Invalid RPC path format")
		return
	}

	schemaName := "public"
	functionName := pathParts[0]
	if len(pathParts) > 1 {
		schemaName = pathParts[0]
		functionName = pathParts[1]
	}

	// Only allow POST for function calls
	if r.Method != http.MethodPost {
		httputil.Error(w, http.StatusMethodNotAllowed, "Only POST method allowed for RPC calls")
		return
	}

	// Get function metadata
	function, err := s.getFunction(r.Context(), conn, schemaName, functionName)
	if err != nil {
		httputil.Error(w, http.StatusNotFound, fmt.Sprintf("Function %s.%s not found: %v", schemaName, functionName, err))
		return
	}

	s.handleFunctionCall(w, r, function)
}

// getFunction retrieves function metadata from PostgreSQL
func (s *Server) getFunction(ctx context.Context, conn *pgxpool.Conn, schemaName, functionName string) (*Function, error) {
	query := `
		SELECT 
			p.proname as function_name,
			n.nspname as schema_name,
			p.proretset as is_setof,
			t.typname as return_type,
			p.prolang as language_oid,
			CASE WHEN p.prokind = 'a' THEN true ELSE false END as is_aggregate,
			p.pronargs as num_args,
			p.proargnames as arg_names,
			p.proargtypes::text as arg_types,
			p.proargmodes as arg_modes,
			p.proargdefaults as arg_defaults
		FROM pg_proc p
		JOIN pg_namespace n ON p.pronamespace = n.oid
		JOIN pg_type t ON p.prorettype = t.oid
		WHERE n.nspname = $1 AND p.proname = $2
		AND p.prokind = 'f'  -- Only functions, not procedures
	`

	row := conn.QueryRow(ctx, query, schemaName, functionName)

	var (
		funcName, schema, returnType string
		isSetof, isAggregate         bool
		languageOid                  uint32
		numArgs                      int
		argNames                     *[]string
		argTypes                     *string
		argModes                     *[]string
		argDefaults                  *string
	)

	err := row.Scan(&funcName, &schema, &isSetof, &returnType, &languageOid, &isAggregate, &numArgs, &argNames, &argTypes, &argModes, &argDefaults)
	if err != nil {
		return nil, err
	}

	function := &Function{
		Schema:      schema,
		Name:        funcName,
		ReturnType:  returnType,
		IsSetof:     isSetof,
		IsAggregate: isAggregate,
		Parameters:  make([]FunctionParam, 0),
	}

	// Parse parameters if any exist
	if numArgs > 0 {
		params, err := s.parseFunctionParameters(ctx, conn, argNames, argTypes, argModes, argDefaults, numArgs)
		if err != nil {
			return nil, fmt.Errorf("failed to parse parameters: %w", err)
		}
		function.Parameters = params
	}

	return function, nil
}

// parseFunctionParameters parses the PostgreSQL function parameter arrays
func (s *Server) parseFunctionParameters(ctx context.Context, conn *pgxpool.Conn, argNames, argTypes, argModes, argDefaults any, numArgs int) ([]FunctionParam, error) {
	params := make([]FunctionParam, numArgs)

	// Get type names for the argument types
	var typeOids []int64
	if argTypes != nil {
		if oidVector, ok := argTypes.(string); ok {
			// Parse the oidvector (space-separated OIDs)
			oidStrs := strings.Fields(oidVector)
			typeOids = make([]int64, len(oidStrs))
			for i, oidStr := range oidStrs {
				oid, err := strconv.ParseInt(oidStr, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("invalid type OID: %s", oidStr)
				}
				typeOids[i] = oid
			}
		}
	}

	// Get type names from OIDs
	typeNames := make([]string, len(typeOids))
	if len(typeOids) > 0 {
		typeQuery := `SELECT oid, typname FROM pg_type WHERE oid = ANY($1)`
		rows, err := conn.Query(ctx, typeQuery, typeOids)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		typeMap := make(map[int64]string)
		for rows.Next() {
			var oid int64
			var typeName string
			if err := rows.Scan(&oid, &typeName); err != nil {
				return nil, err
			}
			typeMap[oid] = typeName
		}

		for i, oid := range typeOids {
			if typeName, ok := typeMap[oid]; ok {
				typeNames[i] = typeName
			} else {
				typeNames[i] = "unknown"
			}
		}
	}

	// Parse argument names if available
	var names []string
	if argNames != nil {
		if nameArray, ok := argNames.([]any); ok {
			names = make([]string, len(nameArray))
			for i, name := range nameArray {
				if nameStr, ok := name.(string); ok {
					names[i] = nameStr
				} else {
					names[i] = fmt.Sprintf("arg%d", i+1)
				}
			}
		}
	}

	// Parse argument modes if available
	var modes []string
	if argModes != nil {
		if modeArray, ok := argModes.([]any); ok {
			modes = make([]string, len(modeArray))
			for i, mode := range modeArray {
				if modeStr, ok := mode.(string); ok {
					switch modeStr {
					case "i":
						modes[i] = "IN"
					case "o":
						modes[i] = "OUT"
					case "b":
						modes[i] = "INOUT"
					case "v":
						modes[i] = "VARIADIC"
					default:
						modes[i] = "IN"
					}
				} else {
					modes[i] = "IN"
				}
			}
		}
	}

	// Build parameter list
	for i := range numArgs {
		param := FunctionParam{
			Position: i + 1,
			DataType: "text", // default
			Mode:     "IN",   // default
		}

		if i < len(names) && names[i] != "" {
			param.Name = names[i]
		} else {
			param.Name = fmt.Sprintf("arg%d", i+1)
		}

		if i < len(typeNames) {
			param.DataType = typeNames[i]
		}

		if i < len(modes) {
			param.Mode = modes[i]
		}

		params[i] = param
	}

	return params, nil
}

// handleFunctionCall executes the PostgreSQL function with provided arguments
func (s *Server) handleFunctionCall(w http.ResponseWriter, r *http.Request, function *Function) {
	// Parse JSON body for function arguments
	var args map[string]any

	// For functions with no parameters, allow empty body
	if r.ContentLength == 0 || (r.Body != nil && r.ContentLength == -1) {
		// Try to read the body to see if it's empty
		body, err := io.ReadAll(r.Body)
		if err != nil {
			httputil.Error(w, http.StatusBadRequest, "Error reading request body")
			return
		}

		if len(body) == 0 {
			args = make(map[string]any)
		} else {
			if err := json.Unmarshal(body, &args); err != nil {
				httputil.Error(w, http.StatusBadRequest, "Invalid JSON body")
				return
			}
		}
	} else {
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			httputil.Error(w, http.StatusBadRequest, "Invalid JSON body")
			return
		}
	}

	// Build function call query
	query, queryArgs, err := s.buildFunctionCallQuery(function, args)
	if err != nil {
		httputil.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	// Execute the function
	s.executeFunctionQuery(w, r, query, queryArgs, function.IsSetof)
}

// buildFunctionCallQuery constructs the SQL query to call the function
func (s *Server) buildFunctionCallQuery(function *Function, args map[string]any) (string, []any, error) {
	var queryArgs []any
	var placeholders []string

	// Filter only IN and INOUT parameters
	inputParams := make([]FunctionParam, 0)
	for _, param := range function.Parameters {
		if param.Mode == "IN" || param.Mode == "INOUT" {
			inputParams = append(inputParams, param)
		}
	}

	// Build arguments in parameter order
	for i, param := range inputParams {
		argValue, exists := args[param.Name]
		if !exists {
			// Check for positional arguments
			if argValue, exists = args[fmt.Sprintf("arg%d", i+1)]; !exists {
				if param.DefaultValue != nil {
					// Skip parameters with defaults if not provided
					continue
				}
				return "", nil, fmt.Errorf("missing required parameter: %s", param.Name)
			}
		}

		// Convert argument to appropriate type
		convertedValue, err := s.convertArgumentType(argValue, param.DataType)
		if err != nil {
			return "", nil, fmt.Errorf("invalid value for parameter %s: %v", param.Name, err)
		}

		queryArgs = append(queryArgs, convertedValue)
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(queryArgs)))
	}

	// Build the query
	var query string
	if function.IsSetof {
		// Function returns a table/set
		query = fmt.Sprintf(`SELECT * FROM "%s"."%s"(%s)`,
			function.Schema, function.Name, strings.Join(placeholders, ", "))
	} else {
		// Function returns a single value
		query = fmt.Sprintf(`SELECT "%s"."%s"(%s) as result`,
			function.Schema, function.Name, strings.Join(placeholders, ", "))
	}

	return query, queryArgs, nil
}

// convertArgumentType converts JSON values to appropriate PostgreSQL types
func (s *Server) convertArgumentType(value any, dataType string) (any, error) {
	if value == nil {
		return nil, nil
	}

	fmt.Println("dataType", dataType)

	switch dataType {
	case "integer", "int4", "int8", "bigint", "smallint":
		switch v := value.(type) {
		case float64:
			return int64(v), nil
		case string:
			return strconv.ParseInt(v, 10, 64)
		case int:
			return int64(v), nil
		default:
			return nil, fmt.Errorf("cannot convert %T to integer", value)
		}
	case "numeric", "decimal", "real", "double precision", "float4", "float8":
		switch v := value.(type) {
		case float64:
			return v, nil
		case string:
			return strconv.ParseFloat(v, 64)
		case int:
			return float64(v), nil
		default:
			return nil, fmt.Errorf("cannot convert %T to numeric", value)
		}
	case "boolean", "bool":
		switch v := value.(type) {
		case bool:
			return v, nil
		case string:
			return strconv.ParseBool(v)
		default:
			return nil, fmt.Errorf("cannot convert %T to boolean", value)
		}
	case "json", "jsonb":
		// For JSON types, convert back to JSON string
		if reflect.TypeOf(value).Kind() == reflect.String {
			return value, nil
		}
		jsonBytes, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("cannot convert to JSON: %v", err)
		}
		return string(jsonBytes), nil
	case "uuid":
		return fmt.Sprintf("\"%s\"::TEXT", value), nil
	default:
		// For text, varchar, uuid, etc., convert to string
		return fmt.Sprintf("%v", value), nil
	}
}

// executeFunctionQuery executes the function query and returns results
func (s *Server) executeFunctionQuery(w http.ResponseWriter, r *http.Request, query string, args []any, isSetof bool) {
	_, conn, pgErr := httputil.ConnWithRole(r)
	if pgErr != nil {
		httputil.Error(w, http.StatusInternalServerError, pgErr.Error())
		return
	}
	defer conn.Release()

	rows, err := conn.Query(r.Context(), query, args...)
	if err != nil {
		httputil.Error(w, http.StatusInternalServerError, fmt.Sprintf("Function execution failed: %v", err))
		return
	}
	defer rows.Close()

	var results []map[string]any
	if s.omitempty {
		results, err = collectRowsOmitNull(rows)
		if err != nil {
			httputil.Error(w, http.StatusInternalServerError, fmt.Sprintf("Failed to collect results: %v", err))
			return
		}
	} else {
		results, err = pgx.CollectRows(rows, pgx.RowToMap)
		if err != nil {
			httputil.Error(w, http.StatusInternalServerError, fmt.Sprintf("Failed to collect results: %v", err))
			return
		}
	}

	// For non-setof functions, return just the result value if it's a single column
	if !isSetof && len(results) == 1 && len(results[0]) == 1 {
		for _, value := range results[0] {
			httputil.JSON(w, http.StatusOK, value)
			return
		}
	}

	httputil.JSON(w, http.StatusOK, results)
}
