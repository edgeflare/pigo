package rest

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/edgeflare/pigo/pkg/pgx/schema"
	"github.com/jackc/pgx/v5"
)

// QueryParams holds parsed query parameters in a structured way
type QueryParams struct {
	Select     []string                 // Columns to select
	Order      []OrderParam             // Order by columns
	Limit      int                      // Limit results
	Offset     int                      // Offset results
	Filters    map[string][]FilterParam // Column filters
	EmbedJoins []JoinParam              // Embedded resource joins (foreign keys)
}

type JoinParam struct {
	Table    string
	JoinType string
	On       string
}

// Parse query parameters from request
func parseQueryParams(r *http.Request) QueryParams {
	params := QueryParams{
		Filters: make(map[string][]FilterParam),
	}

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

	// Parse filter parameters (everything else)
	for key, values := range queryValues {
		if isReservedParam(key) {
			continue
		}

		// Handle column filters
		if len(values) > 0 {
			params.Filters[key] = parseFilterParam(values[0])
		}
	}

	return params
}

// Parse select parameter, supports nested selects for embedding resources
func parseSelectParam(select_ string) []string {
	return strings.Split(select_, ",")
}

type FilterParam struct {
	Operator string
	Value    any
}

// Parse filter parameter
func parseFilterParam(value string) []FilterParam {
	result := make([]FilterParam, 0)

	// PostgREST supports these operators: eq, gt, lt, gte, lte, neq, like, ilike, in, is, fts, plfts, phfts
	operators := map[string]string{
		"eq":    "=",
		"gt":    ">",
		"lt":    "<",
		"gte":   ">=",
		"lte":   "<=",
		"neq":   "!=",
		"like":  "LIKE",
		"ilike": "ILIKE",
		"in":    "IN",
		"is":    "IS",
		"fts":   "@@",
		"plfts": "@@",
		"phfts": "@@",
		"cs":    "@>",
		"cd":    "<@",
	}

	// Skip split by comma if this is a containment operator
	// https://www.postgresql.org/docs/current/datatype-json.html#JSON-CONTAINMENT
	hasContainmentOperator := strings.HasPrefix(value, "cs.") || strings.HasPrefix(value, "cd.")

	var orParts []string
	if hasContainmentOperator {
		orParts = []string{value}
	} else {
		orParts = strings.Split(value, ",")
	}

	for _, orPart := range orParts {
		// Default to equality if no operator is specified
		operator := "="
		val := orPart

		// Check for operators
		for op, sqlOp := range operators {
			if strings.HasPrefix(orPart, op+".") {
				operator = sqlOp
				val = orPart[len(op)+1:]
				break
			}
		}

		// Handle IS NULL and IS NOT NULL
		var filterValue any
		if val == "null" && operator == "=" {
			operator = "IS"
			filterValue = nil
		} else if val == "null" && operator == "!=" {
			operator = "IS NOT"
			filterValue = nil
		} else if operator == "@>" || operator == "<@" {
			// Handle array/JSONB containment operators
			filterValue = convertToPostgresArrayOrJSON(val)
		} else {
			filterValue = val
		}

		result = append(result, FilterParam{
			Operator: operator,
			Value:    filterValue,
		})
	}

	return result
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
	}
	return reserved[name]
}

// Build SELECT query from table schema and query parameters with type casting
func buildSelectQuery(table schema.Table, params QueryParams) (string, []any, error) {
	// Initialize query builder
	var query strings.Builder
	var args []any
	var argIndex int = 1

	// Start SELECT statement
	query.WriteString("SELECT ")

	// Add columns to select with type casting where needed
	if len(params.Select) > 0 {
		columnList := make([]string, 0, len(params.Select))
		for _, col := range params.Select {
			// Verify column exists in schema
			var found bool
			var dataType string
			for _, schemaCol := range table.Columns {
				if schemaCol.Name == col {
					found = true
					dataType = schemaCol.DataType
					break
				}
			}
			if found {
				columnExpr := columnCastExpression(col, dataType)
				columnList = append(columnList, columnExpr)
			}
		}
		query.WriteString(strings.Join(columnList, ", "))
	} else {
		// Select all columns with type casting
		columnList := make([]string, 0, len(table.Columns))
		for _, col := range table.Columns {
			columnExpr := columnCastExpression(col.Name, col.DataType)
			columnList = append(columnList, columnExpr)
		}

		if len(columnList) > 0 {
			query.WriteString(strings.Join(columnList, ", "))
		} else {
			query.WriteString("*")
		}
	}

	// Add FROM clause
	query.WriteString(fmt.Sprintf(" FROM \"%s\".\"%s\"", table.Schema, table.Name))

	// Add WHERE clause for filters
	if len(params.Filters) > 0 {
		query.WriteString(" WHERE ")
		whereClauses := make([]string, 0)
		for column, filters := range params.Filters {
			// Verify column exists
			found := false
			for _, schemaCol := range table.Columns {
				if schemaCol.Name == column {
					found = true
					break
				}
			}
			if !found {
				continue
			}

			// Group filters for this column
			columnFilters := make([]string, 0)
			for _, filter := range filters {
				if filter.Value == nil {
					// Handle IS NULL and IS NOT NULL
					columnFilters = append(columnFilters, fmt.Sprintf("\"%s\" %s NULL", column, filter.Operator))
				} else if filter.Operator == "IN" {
					// Handle IN operator
					inValues := strings.Split(filter.Value.(string), ",")
					placeholders := make([]string, len(inValues))
					for i, val := range inValues {
						placeholders[i] = fmt.Sprintf("$%d", argIndex)
						args = append(args, val)
						argIndex++
					}
					columnFilters = append(columnFilters, fmt.Sprintf("\"%s\" IN (%s)", column, strings.Join(placeholders, ", ")))
				} else {
					// Standard operators
					columnFilters = append(columnFilters, fmt.Sprintf("\"%s\" %s $%d", column, filter.Operator, argIndex))
					args = append(args, filter.Value)
					argIndex++
				}
			}

			if len(columnFilters) > 0 {
				// Combine with OR for multiple filters on same column
				whereClauses = append(whereClauses, fmt.Sprintf("(%s)", strings.Join(columnFilters, " OR ")))
			}
		}

		if len(whereClauses) > 0 {
			// Combine with AND for different columns
			query.WriteString(strings.Join(whereClauses, " AND "))
		} else {
			// Remove WHERE if no valid filters
			query.Reset()

			// Rebuild SELECT with type casting
			query.WriteString("SELECT ")
			if len(params.Select) > 0 {
				columnList := make([]string, 0, len(params.Select))
				for _, col := range params.Select {
					// Find column data type
					var dataType string
					for _, schemaCol := range table.Columns {
						if schemaCol.Name == col {
							dataType = schemaCol.DataType
							break
						}
					}
					columnExpr := columnCastExpression(col, dataType)
					columnList = append(columnList, columnExpr)
				}
				query.WriteString(strings.Join(columnList, ", "))
			} else {
				// Select all columns with type casting
				columnList := make([]string, 0, len(table.Columns))
				for _, col := range table.Columns {
					columnExpr := columnCastExpression(col.Name, col.DataType)
					columnList = append(columnList, columnExpr)
				}

				if len(columnList) > 0 {
					query.WriteString(strings.Join(columnList, ", "))
				} else {
					query.WriteString("*")
				}
			}

			query.WriteString(fmt.Sprintf(" FROM \"%s\".\"%s\"", table.Schema, table.Name))
		}
	}

	// Add ORDER BY clause
	if len(params.Order) > 0 {
		query.WriteString(" ORDER BY ")
		orderClauses := make([]string, 0, len(params.Order))

		for _, order := range params.Order {
			if order.Similarity != "" {
				found := false
				for _, schemaCol := range table.Columns {
					if schemaCol.Name == order.Column {
						found = true
						break
					}
				}

				if found {
					// build similarity function call
					similarityClause := fmt.Sprintf("similarity(\"%s\", '%s') %s NULLS %s",
						order.Column,
						strings.ReplaceAll(order.Similarity, "'", "''"), // TODO: sanitize/escapeSQLString
						strings.ToUpper(order.Direction),
						strings.ToUpper(order.NullsPosition))
					orderClauses = append(orderClauses, similarityClause)
				}
			} else {
				// regular ordering by column name
				found := false
				for _, schemaCol := range table.Columns {
					if schemaCol.Name == order.Column {
						found = true
						break
					}
				}

				if found {
					orderClauses = append(orderClauses, fmt.Sprintf("\"%s\" %s NULLS %s",
						order.Column,
						strings.ToUpper(order.Direction),
						strings.ToUpper(order.NullsPosition)))
				}
			}
		}

		if len(orderClauses) > 0 {
			query.WriteString(strings.Join(orderClauses, ", "))
		}
	}

	// Add LIMIT clause
	if params.Limit > 0 {
		query.WriteString(fmt.Sprintf(" LIMIT $%d", argIndex))
		args = append(args, params.Limit)
		argIndex++
	}

	// Add OFFSET clause
	if params.Offset > 0 {
		query.WriteString(fmt.Sprintf(" OFFSET $%d", argIndex))
		args = append(args, params.Offset)
		argIndex++
	}

	return query.String(), args, nil
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
	var args []any
	var argIndex int = 1

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
			setClauses = append(setClauses, fmt.Sprintf("\"%s\" = $%d", key, argIndex))
			args = append(args, value)
			argIndex++
		}
	}

	if len(setClauses) == 0 {
		return "", nil, fmt.Errorf("no valid columns to update")
	}

	query.WriteString(strings.Join(setClauses, ", "))

	// Add WHERE clause for filters
	if len(params.Filters) > 0 {
		query.WriteString(" WHERE ")

		whereClauses := make([]string, 0)
		for column, filters := range params.Filters {
			// Verify column exists
			found := false
			for _, schemaCol := range table.Columns {
				if schemaCol.Name == column {
					found = true
					break
				}
			}
			if !found {
				continue
			}

			// Group filters for this column
			columnFilters := make([]string, 0)
			for _, filter := range filters {
				if filter.Value == nil {
					// Handle IS NULL and IS NOT NULL
					columnFilters = append(columnFilters, fmt.Sprintf("\"%s\" %s NULL", column, filter.Operator))
				} else if filter.Operator == "IN" {
					// Handle IN operator
					inValues := strings.Split(filter.Value.(string), ",")
					placeholders := make([]string, len(inValues))
					for i, val := range inValues {
						placeholders[i] = fmt.Sprintf("$%d", argIndex)
						args = append(args, val)
						argIndex++
					}
					columnFilters = append(columnFilters, fmt.Sprintf("\"%s\" IN (%s)", column, strings.Join(placeholders, ", ")))
				} else {
					// Standard operators
					columnFilters = append(columnFilters, fmt.Sprintf("\"%s\" %s $%d", column, filter.Operator, argIndex))
					args = append(args, filter.Value)
					argIndex++
				}
			}

			if len(columnFilters) > 0 {
				// Combine with OR for multiple filters on same column
				whereClauses = append(whereClauses, fmt.Sprintf("(%s)", strings.Join(columnFilters, " OR ")))
			}
		}

		if len(whereClauses) > 0 {
			// Combine with AND for different columns
			query.WriteString(strings.Join(whereClauses, " AND "))
		}
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
	var args []any
	var argIndex int = 1

	// Start DELETE statement
	query.WriteString(fmt.Sprintf("DELETE FROM \"%s\".\"%s\"", table.Schema, table.Name))

	// Add WHERE clause for filters
	if len(params.Filters) > 0 {
		query.WriteString(" WHERE ")

		whereClauses := make([]string, 0)
		for column, filters := range params.Filters {
			// Verify column exists
			found := false
			for _, schemaCol := range table.Columns {
				if schemaCol.Name == column {
					found = true
					break
				}
			}
			if !found {
				continue
			}

			// Group filters for this column
			columnFilters := make([]string, 0)
			for _, filter := range filters {
				if filter.Value == nil {
					// Handle IS NULL and IS NOT NULL
					columnFilters = append(columnFilters, fmt.Sprintf("\"%s\" %s NULL", column, filter.Operator))
				} else if filter.Operator == "IN" {
					// Handle IN operator
					inValues := strings.Split(filter.Value.(string), ",")
					placeholders := make([]string, len(inValues))
					for i, val := range inValues {
						placeholders[i] = fmt.Sprintf("$%d", argIndex)
						args = append(args, val)
						argIndex++
					}
					columnFilters = append(columnFilters, fmt.Sprintf("\"%s\" IN (%s)", column, strings.Join(placeholders, ", ")))
				} else {
					// Standard operators
					columnFilters = append(columnFilters, fmt.Sprintf("\"%s\" %s $%d", column, filter.Operator, argIndex))
					args = append(args, filter.Value)
					argIndex++
				}
			}

			if len(columnFilters) > 0 {
				// Combine with OR for multiple filters on same column
				whereClauses = append(whereClauses, fmt.Sprintf("(%s)", strings.Join(columnFilters, " OR ")))
			}
		}

		if len(whereClauses) > 0 {
			// Combine with AND for different columns
			query.WriteString(strings.Join(whereClauses, " AND "))
		}
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
