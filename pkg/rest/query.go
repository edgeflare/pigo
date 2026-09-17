package rest

import (
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/edgeflare/pigo/pkg/pgx/schema"
	"github.com/jackc/pgx/v5"
)

// QueryParams holds parsed query parameters in a structured way
type QueryParams struct {
	Select     []string     // Columns to select
	Order      []OrderParam // Order by columns
	Limit      int          // Limit results
	Offset     int          // Offset results
	Filters    []RawFilter  // Horizontal filters (column filters and and/or trees), AND-ed together
	EmbedJoins []JoinParam  // Embedded resource joins (foreign keys)
}

type JoinParam struct {
	Table    string
	JoinType string
	On       string
}

// Parse query parameters from request
func parseQueryParams(r *http.Request) QueryParams {
	params := QueryParams{}

	// Get URL query parameters
	queryValues := r.URL.Query()

	// Parse select parameter
	if select_ := queryValues.Get("select"); select_ != "" {
		params.Select = parseSelectParam(select_)
	}

	// Parse order parameter
	if order := queryValues.Get("order"); order != "" {
		params.Order = parseOrderParam(order)
	}

	// Parse limit parameter
	if limit := queryValues.Get("limit"); limit != "" {
		params.Limit = parseIntParam(limit, 100) // Default to 100 if invalid
	} else {
		params.Limit = 100 // Default limit
	}

	// Parse offset parameter
	if offset := queryValues.Get("offset"); offset != "" {
		params.Offset = parseIntParam(offset, 0)
	}

	// Everything else is a filter: column filters (age=gte.18) and logic trees
	// (or=(...), not.and=(...)). They're parsed and checked against the table
	// when the query is built, so malformed filters become 400 errors there.
	// Repeated keys (?age=gte.18&age=lte.65) are all applied, as in PostgREST;
	// keys are sorted so the generated SQL is deterministic.
	for _, key := range slices.Sorted(maps.Keys(queryValues)) {
		if isReservedParam(key) {
			continue
		}
		for _, value := range queryValues[key] {
			params.Filters = append(params.Filters, RawFilter{Key: key, Value: value})
		}
	}

	return params
}

// Parse select parameter, supports nested selects for embedding resources
func parseSelectParam(select_ string) []string {
	return strings.Split(select_, ",")
}

// Parse integer parameter with default value
func parseIntParam(value string, defaultValue int) int {
	var result int
	_, err := fmt.Sscanf(value, "%d", &result)
	if err != nil {
		return defaultValue
	}
	return result
}

// Check if parameter name is a reserved keyword
func isReservedParam(name string) bool {
	reserved := map[string]bool{
		"select": true,
		"order":  true,
		"limit":  true,
		"offset": true,
		// PostgREST also reserves these (POST/PATCH column list, upsert target).
		// "and" / "or" are not reserved: they're logic-tree filters.
		"columns":     true,
		"on_conflict": true,
	}
	return reserved[name]
}

// Build SELECT query from table schema and query parameters with type casting
func buildSelectQuery(table schema.Table, params QueryParams) (string, []any, error) {
	var query strings.Builder
	var args argList

	query.WriteString("SELECT ")
	query.WriteString(selectList(table, params.Select))
	query.WriteString(" FROM ")
	query.WriteString(pgx.Identifier{table.Schema, table.Name}.Sanitize())

	where, err := buildWhereClause(table, params.Filters, &args)
	if err != nil {
		return "", nil, err
	}
	if where != "" {
		query.WriteString(" WHERE ")
		query.WriteString(where)
	}

	orderBy, err := buildOrderByClause(table, params.Order, &args)
	if err != nil {
		return "", nil, err
	}
	if orderBy != "" {
		query.WriteString(" ORDER BY ")
		query.WriteString(orderBy)
	}

	if params.Limit > 0 {
		query.WriteString(" LIMIT ")
		query.WriteString(args.add(params.Limit))
	}
	if params.Offset > 0 {
		query.WriteString(" OFFSET ")
		query.WriteString(args.add(params.Offset))
	}

	return query.String(), args, nil
}

// selectList renders the SELECT column list with type casting. "*" (also
// PostgREST's default) expands to every column; unknown columns are skipped.
func selectList(table schema.Table, selected []string) string {
	if len(selected) == 0 {
		if len(table.Columns) == 0 {
			return "*"
		}
		selected = []string{"*"}
	}
	columnList := make([]string, 0, len(table.Columns))
	for _, name := range selected {
		if name == "*" {
			for _, col := range table.Columns {
				columnList = append(columnList, columnCastExpression(col.Name, col.DataType))
			}
		} else if col, ok := findColumn(table, name); ok {
			columnList = append(columnList, columnCastExpression(col.Name, col.DataType))
		}
	}
	return strings.Join(columnList, ", ")
}

var (
	orderDirections = map[string]string{"asc": "ASC", "desc": "DESC"}
	nullsPositions  = map[string]string{"first": "NULLS FIRST", "last": "NULLS LAST"}
)

// buildOrderByClause renders the ORDER BY list (without the keywords).
// Unknown columns are an error, as in PostgREST, rather than invalid SQL or
// silently unordered results.
func buildOrderByClause(table schema.Table, order []OrderParam, args *argList) (string, error) {
	clauses := make([]string, 0, len(order))
	for _, o := range order {
		col, ok := findColumn(table, o.Column)
		if !ok {
			return "", fmt.Errorf("column %s.%s does not exist", table.Name, o.Column)
		}
		direction, okDir := orderDirections[strings.ToLower(o.Direction)]
		nulls, okNulls := nullsPositions[strings.ToLower(o.NullsPosition)]
		if !okDir || !okNulls {
			return "", fmt.Errorf("invalid ordering for column %s", o.Column)
		}
		expr := quoteIdent(col.Name)
		if o.Similarity != "" {
			// pg_trgm similarity(); the search string is a bind parameter
			expr = "similarity(" + expr + ", " + args.add(o.Similarity) + ")"
		}
		clauses = append(clauses, expr+" "+direction+" "+nulls)
	}
	return strings.Join(clauses, ", "), nil
}

// Build INSERT query from table schema and data
func buildInsertQuery(table schema.Table, data map[string]any, headers *Headers) (string, []any, error) {
	// Initialize query builder
	var query strings.Builder
	var args []any
	var argIndex int = 1

	// Start INSERT statement
	query.WriteString(fmt.Sprintf("INSERT INTO \"%s\".\"%s\" (", table.Schema, table.Name))

	// Add columns and values
	columns := make([]string, 0, len(data))
	placeholders := make([]string, 0, len(data))

	for key, value := range data {
		// Verify column exists in schema
		found := false
		for _, col := range table.Columns {
			if col.Name == key {
				found = true
				break
			}
		}

		if found {
			columns = append(columns, fmt.Sprintf("\"%s\"", key))
			placeholders = append(placeholders, fmt.Sprintf("$%d", argIndex))
			args = append(args, value)
			argIndex++
		}
	}

	// Complete the query
	query.WriteString(strings.Join(columns, ", "))
	query.WriteString(") VALUES (")
	query.WriteString(strings.Join(placeholders, ", "))
	query.WriteString(")")

	// Add RETURNING clause only if requested
	if headers != nil && headers.Prefer != nil && headers.Prefer.WantsRepresentation() {
		query.WriteString(" RETURNING ")

		returningCols := make([]string, 0, len(table.Columns))
		for _, col := range table.Columns {
			returningCols = append(returningCols, columnCastExpression(col.Name, col.DataType))
		}

		query.WriteString(strings.Join(returningCols, ", "))
	}

	return query.String(), args, nil
}

// Build UPDATE query from table schema, data, and query parameters
func buildUpdateQuery(table schema.Table, data map[string]any, params QueryParams, headers *Headers) (string, []any, error) {
	// Initialize query builder
	var query strings.Builder
	var args argList

	// Start UPDATE statement
	query.WriteString(fmt.Sprintf("UPDATE \"%s\".\"%s\" SET ", table.Schema, table.Name))

	// Add SET clause
	setClauses := make([]string, 0, len(data))
	for key, value := range data {
		// Verify column exists in schema
		found := false
		for _, col := range table.Columns {
			if col.Name == key {
				found = true
				break
			}
		}

		if found {
			setClauses = append(setClauses, fmt.Sprintf("\"%s\" = %s", key, args.add(value)))
		}
	}

	if len(setClauses) == 0 {
		return "", nil, fmt.Errorf("no valid columns to update")
	}

	query.WriteString(strings.Join(setClauses, ", "))

	// Add WHERE clause for filters (same PostgREST filtering as SELECT)
	where, err := buildWhereClause(table, params.Filters, &args)
	if err != nil {
		return "", nil, err
	}
	if where != "" {
		query.WriteString(" WHERE ")
		query.WriteString(where)
	}

	// Add RETURNING clause only if requested (to return the updated row/s)
	if headers != nil && headers.Prefer != nil && headers.Prefer.WantsRepresentation() {
		query.WriteString(" RETURNING ")

		returningCols := make([]string, 0, len(table.Columns))
		for _, col := range table.Columns {
			returningCols = append(returningCols, columnCastExpression(col.Name, col.DataType))
		}

		query.WriteString(strings.Join(returningCols, ", "))
	}

	return query.String(), args, nil
}

// Build DELETE query from table schema and query parameters
func buildDeleteQuery(table schema.Table, params QueryParams, headers *Headers) (string, []any, error) {
	// Initialize query builder
	var query strings.Builder
	var args argList

	// Start DELETE statement
	query.WriteString(fmt.Sprintf("DELETE FROM \"%s\".\"%s\"", table.Schema, table.Name))

	// Add WHERE clause for filters (same PostgREST filtering as SELECT)
	where, err := buildWhereClause(table, params.Filters, &args)
	if err != nil {
		return "", nil, err
	}
	if where != "" {
		query.WriteString(" WHERE ")
		query.WriteString(where)
	}

	// Add RETURNING clause only if requested (to return the deleted row/s)
	if headers != nil && headers.Prefer != nil && headers.Prefer.WantsRepresentation() {
		query.WriteString(" RETURNING ")

		returningCols := make([]string, 0, len(table.Columns))
		for _, col := range table.Columns {
			returningCols = append(returningCols, columnCastExpression(col.Name, col.DataType))
		}

		query.WriteString(strings.Join(returningCols, ", "))
	}

	return query.String(), args, nil
}

// columnCastExpression returns appropriate type casting of a column used in SELECT query
func columnCastExpression(columnName string, dataType string) string {
	switch dataType {
	case "uuid":
		return fmt.Sprintf("\"%s\"::TEXT as \"%s\"", columnName, columnName)
	case "time without time zone":
		return fmt.Sprintf("\"%s\"::TEXT as \"%s\"", columnName, columnName)
	case "date":
		return fmt.Sprintf("\"%s\"::TEXT as \"%s\"", columnName, columnName)
	default:
		return fmt.Sprintf("\"%s\"", columnName)
	}
}

// collectRowsOmitNull collects rows and omits NULL values from the result maps
// This is equivalent to using json:"omitempty" tag in Go structs
func collectRowsOmitNull(rows pgx.Rows) ([]map[string]any, error) {
	var results []map[string]any

	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, err
		}

		fieldDescriptions := rows.FieldDescriptions()

		// Create a map for this row, omitting NULL values
		rowMap := make(map[string]any)
		for i, value := range values {
			if value != nil {
				columnName := string(fieldDescriptions[i].Name)
				rowMap[columnName] = value
			}
		}

		results = append(results, rowMap)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return results, nil
}
