package rest

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/edgeflare/pigo/pkg/pgx/schema"
	"github.com/jackc/pgx/v5"
)

// PostgREST-compatible horizontal filtering.
// https://docs.postgrest.org/en/v16/references/api/tables_views.html#horizontal-filtering
//
//	column filter  ?<field>=[not.]<op>.<value>          ?age=gte.18  ?id=not.in.(1,2)
//	logic tree     ?[not.]<and|or>=(<cond>,<cond>,...)  ?or=(age.lt.18,age.gt.21)
//	cond           <field>.[not.]<op>.<value>
//	               [not.]<and|or>(<cond>,...)           ?or=(a.eq.1,not.and(b.gt.2,c.is.null))
//	field          <column>[->key|->>key|->0]...        ?data->>status=eq.active
//
// All filters of a request are AND-ed. Values are always bind parameters and
// columns are checked against the schema cache, so a filter can neither inject
// SQL nor be silently dropped (which would widen a SELECT, UPDATE or DELETE).

// RawFilter is one filter query parameter as sent by the client, e.g.
// {Key: "age", Value: "gte.18"} or {Key: "or", Value: "(a.eq.1,b.eq.2)"}.
// It is parsed and validated when the query is built (see buildWhereClause).
type RawFilter struct {
	Key   string
	Value string
}

// sqlOperators maps PostgREST operators that take a single operand to SQL.
var sqlOperators = map[string]string{
	"eq":     "=",
	"neq":    "<>",
	"gt":     ">",
	"gte":    ">=",
	"lt":     "<",
	"lte":    "<=",
	"like":   "LIKE",
	"ilike":  "ILIKE",
	"match":  "~",
	"imatch": "~*",
	"cs":     "@>",
	"cd":     "<@",
	"ov":     "&&",
	"sl":     "<<",
	"sr":     ">>",
	"nxr":    "&<",
	"nxl":    "&>",
	"adj":    "-|-",
}

// quantifiable lists the operators accepting the (any) / (all) modifiers.
var quantifiable = map[string]bool{
	"eq": true, "gt": true, "gte": true, "lt": true, "lte": true,
	"like": true, "ilike": true, "match": true, "imatch": true,
}

// ftsFunctions maps full-text search operators to their tsquery constructor.
var ftsFunctions = map[string]string{
	"fts":   "to_tsquery",
	"plfts": "plainto_tsquery",
	"phfts": "phraseto_tsquery",
	"wfts":  "websearch_to_tsquery",
}

// isValues are the operands accepted by the "is" operator.
var isValues = map[string]string{
	"null":     "NULL",
	"not_null": "NOT NULL",
	"true":     "TRUE",
	"false":    "FALSE",
	"unknown":  "UNKNOWN",
}

// maxFilterDepth bounds and/or nesting so a crafted URL can't recurse without limit.
const maxFilterDepth = 32

// filterNode is a parsed filter: either a logical group (logic is "AND" or
// "OR") or a single condition on a field.
type filterNode struct {
	negate bool

	logic    string       // "AND" / "OR" for groups, empty for conditions
	children []filterNode // members of a group

	field    filterField
	operator string   // PostgREST operator: eq, like, in, is, fts, cs, ...
	quant    string   // "ANY" / "ALL" for eq(any), like(all), ...
	lang     string   // text search configuration, fts(english)
	value    string   // operand of every operator except in
	list     []string // operand of in
}

// filterField is a column optionally followed by a JSON path (data->a->>b).
type filterField struct {
	column string
	path   []jsonPathStep
}

type jsonPathStep struct {
	arrow   string // "->" or "->>"
	key     string
	index   int
	isIndex bool
}

// buildWhereClause renders filters as a boolean SQL expression (without the
// WHERE keyword), appending bind parameters to args. It returns "" if there
// are no filters.
func buildWhereClause(table schema.Table, filters []RawFilter, args *argList) (string, error) {
	conds := make([]string, 0, len(filters))
	for _, f := range filters {
		node, err := parseFilter(f.Key, f.Value)
		if err != nil {
			return "", err
		}
		cond, err := node.toSQL(table, args)
		if err != nil {
			return "", err
		}
		conds = append(conds, cond)
	}
	return strings.Join(conds, " AND "), nil
}

// parseFilter parses one filter query parameter. A key whose last
// dot-separated segment is "and" or "or" is a logic tree (or=(...),
// not.and=(...)); any other key is a column filter (age=gte.18).
func parseFilter(key, value string) (filterNode, error) {
	segments := strings.Split(key, ".")
	if last := segments[len(segments)-1]; last == "and" || last == "or" {
		prefix := segments[:len(segments)-1]
		negate := len(prefix) > 0 && prefix[len(prefix)-1] == "not"
		if negate {
			prefix = prefix[:len(prefix)-1]
		}
		if len(prefix) > 0 {
			return filterNode{}, embeddedFilterError(key)
		}
		p := filterParser{src: value}
		node, err := p.group(strings.ToUpper(last), negate, 1)
		if err == nil {
			err = p.expectEOF()
		}
		if err != nil {
			return filterNode{}, fmt.Errorf("failed to parse logic tree (%s): %w", value, err)
		}
		return node, nil
	}

	field, err := parseFilterKey(key)
	if err != nil {
		return filterNode{}, err
	}
	p := filterParser{src: value}
	node, err := p.operation(false)
	if err == nil {
		err = p.expectEOF()
	}
	if err != nil {
		return filterNode{}, fmt.Errorf("failed to parse filter (%s): %w", value, err)
	}
	node.field = field
	return node, nil
}

// parseFilterKey parses the left-hand side of a column filter. PostgREST
// treats dotted keys (child.col) as filters on embedded resources, which
// aren't supported here.
func parseFilterKey(key string) (filterField, error) {
	p := filterParser{src: key}
	column, err := p.name()
	if err == nil && p.peek() == '.' {
		return filterField{}, embeddedFilterError(key)
	}
	var path []jsonPathStep
	if err == nil {
		path, err = p.jsonPath()
	}
	if err == nil {
		err = p.expectEOF()
	}
	if err != nil {
		return filterField{}, fmt.Errorf("failed to parse filter key (%s): %w", key, err)
	}
	return filterField{column: column, path: path}, nil
}

func embeddedFilterError(key string) error {
	return fmt.Errorf("failed to parse filter key (%s): filters on embedded resources are not supported", key)
}

// filterParser is a recursive-descent parser over a filter key or value.
type filterParser struct {
	src string
	pos int
}

// group parses "(cond,cond,...)" for a logic operator whose name has already
// been consumed.
func (p *filterParser) group(logic string, negate bool, depth int) (filterNode, error) {
	if depth > maxFilterDepth {
		return filterNode{}, fmt.Errorf("logic trees can't be nested more than %d levels deep", maxFilterDepth)
	}
	if err := p.expect("("); err != nil {
		return filterNode{}, err
	}
	p.skipSpaces()
	node := filterNode{negate: negate, logic: logic}
	for {
		child, err := p.condition(depth)
		if err != nil {
			return filterNode{}, err
		}
		node.children = append(node.children, child)
		if !p.consume(",") {
			break
		}
		p.skipSpaces()
	}
	if !p.consume(")") {
		return filterNode{}, p.errorf(`expected "," or ")"`)
	}
	p.skipSpaces()
	return node, nil
}

// condition parses one member of a logic tree: a nested group such as
// not.and(...) or a filter such as age.gte.18.
func (p *filterParser) condition(depth int) (filterNode, error) {
	for _, g := range [...]struct {
		prefix, logic string
		negate        bool
	}{
		{"and(", "AND", false},
		{"or(", "OR", false},
		{"not.and(", "AND", true},
		{"not.or(", "OR", true},
	} {
		if strings.HasPrefix(p.src[p.pos:], g.prefix) {
			p.pos += len(g.prefix) - 1 // leave "(" for group
			return p.group(g.logic, g.negate, depth+1)
		}
	}

	column, err := p.name()
	if err != nil {
		return filterNode{}, err
	}
	path, err := p.jsonPath()
	if err != nil {
		return filterNode{}, err
	}
	if err := p.expect("."); err != nil {
		return filterNode{}, err
	}
	node, err := p.operation(true)
	if err != nil {
		return filterNode{}, err
	}
	node.field = filterField{column: column, path: path}
	return node, nil
}

// operation parses "[not.]<op>[(modifier)].<operand>". inTree reports
// whether it is part of a logic tree, where an operand ends at "," or ")".
func (p *filterParser) operation(inTree bool) (filterNode, error) {
	node := filterNode{negate: p.consume("not.")}

	start := p.pos
	for !p.eof() && isASCIILetter(p.src[p.pos]) {
		p.pos++
	}
	op := p.src[start:p.pos]
	node.operator = op

	switch {
	case op == "":
		return node, p.errorf("expected an operator (eq, gt, in, is, ...)")

	case op == "in":
		if err := p.expect("."); err != nil {
			return node, err
		}
		list, err := p.list(inTree)
		node.list = list
		return node, err

	case op == "is":
		if err := p.expect("."); err != nil {
			return node, err
		}
		start := p.pos
		for !p.eof() && (isASCIILetter(p.src[p.pos]) || p.src[p.pos] == '_') {
			p.pos++
		}
		node.value = strings.ToLower(p.src[start:p.pos])
		if _, ok := isValues[node.value]; !ok {
			return node, fmt.Errorf("invalid value %q for is, expected null, not_null, true, false or unknown", p.src[start:p.pos])
		}
		return node, nil

	case op == "isdistinct":
		// operand parsed below

	case ftsFunctions[op] != "":
		if p.consume("(") {
			start := p.pos
			for !p.eof() && (isASCIILetter(p.src[p.pos]) || isASCIIDigit(p.src[p.pos]) || p.src[p.pos] == '_') {
				p.pos++
			}
			node.lang = p.src[start:p.pos]
			if node.lang == "" {
				return node, p.errorf("expected a text search configuration, e.g. fts(english)")
			}
			if err := p.expect(")"); err != nil {
				return node, err
			}
		}

	case sqlOperators[op] != "":
		if p.consume("(") {
			if !quantifiable[op] {
				return node, fmt.Errorf("operator %q does not support the any/all modifiers", op)
			}
			switch {
			case p.consume("any)"):
				node.quant = "ANY"
			case p.consume("all)"):
				node.quant = "ALL"
			default:
				return node, p.errorf(`expected "any)" or "all)"`)
			}
		}

	default:
		return node, fmt.Errorf("unknown operator %q", op)
	}

	if err := p.expect("."); err != nil {
		return node, err
	}
	node.value = p.operand(inTree)
	return node, nil
}

// operand parses a single value. In a column filter it's the rest of the
// input, verbatim: commas, parentheses and quotes are literal, as in PostgREST.
// In a logic tree it ends at "," or ")" unless it's a quoted string ("a,b")
// or a {...} array/object literal.
func (p *filterParser) operand(inTree bool) string {
	if !inTree {
		v := p.src[p.pos:]
		p.pos = len(p.src)
		return v
	}
	if v, ok := p.quotedElement(); ok {
		return v
	}
	if p.peek() == '{' {
		if end := strings.IndexAny(p.src[p.pos+1:], "{}"); end >= 0 && p.src[p.pos+1+end] == '}' {
			v := p.src[p.pos : p.pos+end+2]
			p.pos += end + 2
			return v
		}
	}
	return p.until(",)")
}

// list parses the operand of in: (a,b,"c,d"). An empty list, in.(), matches
// no rows. Column filters also accept this project's older unparenthesized
// form, in.a,b,c.
func (p *filterParser) list(inTree bool) ([]string, error) {
	if !p.consume("(") {
		if inTree || p.eof() {
			return nil, p.errorf(`expected "("`)
		}
		items := strings.Split(p.src[p.pos:], ",")
		p.pos = len(p.src)
		return items, nil
	}
	p.skipSpaces()
	items := []string{}
	for {
		if v, ok := p.quotedElement(); ok {
			items = append(items, v)
		} else {
			item := p.until(",)")
			if item == "" && len(items) == 0 && p.peek() == ')' {
				break // ()
			}
			items = append(items, item)
		}
		if !p.consume(",") {
			break
		}
	}
	if err := p.expect(")"); err != nil {
		return nil, err
	}
	p.skipSpaces()
	return items, nil
}

// name parses a column name or JSON key: a double-quoted string, or a run of
// letters, digits, '_', '$', spaces and inner '-' (never the '-' of "->").
func (p *filterParser) name() (string, error) {
	if p.peek() == '"' {
		s, ok := p.quoted()
		if !ok {
			return "", p.errorf("unterminated quoted name")
		}
		return s, nil
	}
	start := p.pos
	for !p.eof() {
		r, size := utf8.DecodeRuneInString(p.src[p.pos:])
		if isNameRune(r) || (r == '-' && p.pos > start && p.nameRuneAt(p.pos+1)) {
			p.pos += size
			continue
		}
		break
	}
	if p.pos == start {
		return "", p.errorf("expected a column name")
	}
	return p.src[start:p.pos], nil
}

// jsonPath parses zero or more ->key, ->>key or ->0 steps.
func (p *filterParser) jsonPath() ([]jsonPathStep, error) {
	var path []jsonPathStep
	for {
		var step jsonPathStep
		switch {
		case p.consume("->>"):
			step.arrow = "->>"
		case p.consume("->"):
			step.arrow = "->"
		default:
			return path, nil
		}
		if idx, ok := p.arrayIndex(); ok {
			step.index, step.isIndex = idx, true
		} else {
			key, err := p.name()
			if err != nil {
				return nil, err
			}
			step.key = key
		}
		path = append(path, step)
	}
}

// arrayIndex parses a JSON array index such as 0 or -1. It must be a whole
// token, so a key like "2fa" is still a key.
func (p *filterParser) arrayIndex() (int, bool) {
	i := p.pos
	if i < len(p.src) && p.src[i] == '-' {
		i++
	}
	digits := i
	for i < len(p.src) && isASCIIDigit(p.src[i]) {
		i++
	}
	if i == digits || p.nameRuneAt(i) || (i < len(p.src) && p.src[i] == '-' && p.nameRuneAt(i+1)) {
		return 0, false
	}
	n, err := strconv.ParseInt(p.src[p.pos:i], 10, 32)
	if err != nil {
		return 0, false
	}
	p.pos = i
	return int(n), true
}

// quotedElement parses a quoted string if one starts here and is directly
// followed by ",", ")" or the end. Otherwise it consumes nothing and the quote
// is an ordinary character, the same backtracking PostgREST does.
func (p *filterParser) quotedElement() (string, bool) {
	start := p.pos
	if s, ok := p.quoted(); ok && (p.eof() || p.peek() == ',' || p.peek() == ')') {
		return s, true
	}
	p.pos = start
	return "", false
}

// quoted parses a double-quoted string; a backslash escapes the next character.
func (p *filterParser) quoted() (string, bool) {
	if p.peek() != '"' {
		return "", false
	}
	var b strings.Builder
	for i := p.pos + 1; i < len(p.src); i++ {
		switch c := p.src[i]; c {
		case '\\':
			if i+1 == len(p.src) {
				return "", false
			}
			i++
			b.WriteByte(p.src[i])
		case '"':
			p.pos = i + 1
			return b.String(), true
		default:
			b.WriteByte(c)
		}
	}
	return "", false
}

// until consumes and returns the input up to the first byte in stops.
func (p *filterParser) until(stops string) string {
	start := p.pos
	for !p.eof() && strings.IndexByte(stops, p.src[p.pos]) < 0 {
		p.pos++
	}
	return p.src[start:p.pos]
}

func (p *filterParser) eof() bool { return p.pos >= len(p.src) }

func (p *filterParser) peek() byte {
	if p.eof() {
		return 0
	}
	return p.src[p.pos]
}

func (p *filterParser) nameRuneAt(i int) bool {
	if i >= len(p.src) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(p.src[i:])
	return isNameRune(r)
}

func (p *filterParser) consume(s string) bool {
	if strings.HasPrefix(p.src[p.pos:], s) {
		p.pos += len(s)
		return true
	}
	return false
}

func (p *filterParser) expect(s string) error {
	if !p.consume(s) {
		return p.errorf("expected %q", s)
	}
	return nil
}

func (p *filterParser) expectEOF() error {
	if !p.eof() {
		return p.errorf("unexpected input")
	}
	return nil
}

func (p *filterParser) skipSpaces() {
	for !p.eof() && strings.IndexByte(" \t\r\n", p.src[p.pos]) >= 0 {
		p.pos++
	}
}

// errorf returns a syntax error describing what was found at the current position.
func (p *filterParser) errorf(format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	if p.eof() {
		return fmt.Errorf("%s, found end of input", msg)
	}
	r, _ := utf8.DecodeRuneInString(p.src[p.pos:])
	return fmt.Errorf("%s, found %q at position %d", msg, r, p.pos+1)
}

func isNameRune(r rune) bool {
	return unicode.IsLetter(r) || ('0' <= r && r <= '9') || r == '_' || r == '$' || r == ' '
}

func isASCIILetter(c byte) bool { return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' }

func isASCIIDigit(c byte) bool { return '0' <= c && c <= '9' }

// toSQL renders the node. Groups are always parenthesized; negated
// conditions become NOT (...).
func (n filterNode) toSQL(table schema.Table, args *argList) (string, error) {
	var sql string
	if n.logic != "" {
		parts := make([]string, len(n.children))
		for i, child := range n.children {
			part, err := child.toSQL(table, args)
			if err != nil {
				return "", err
			}
			parts[i] = part
		}
		sql = "(" + strings.Join(parts, " "+n.logic+" ") + ")"
	} else {
		cond, err := n.conditionSQL(table, args)
		if err != nil {
			return "", err
		}
		sql = cond
		if n.negate {
			sql = "(" + sql + ")"
		}
	}
	if n.negate {
		sql = "NOT " + sql
	}
	return sql, nil
}

func (n filterNode) conditionSQL(table schema.Table, args *argList) (string, error) {
	col, ok := findColumn(table, n.field.column)
	if !ok {
		return "", fmt.Errorf("column %s.%s does not exist", table.Name, n.field.column)
	}
	lhs := n.field.toSQL(col, args)

	switch n.operator {
	case "in":
		// One array parameter (like PostgREST's = ANY('{...}')): works for any
		// column type, and the SQL text doesn't change with the list length.
		return lhs + " = ANY(" + args.add(pgArrayLiteral(n.list)) + ")", nil
	case "is":
		return lhs + " IS " + isValues[n.value], nil
	case "isdistinct":
		return lhs + " IS DISTINCT FROM " + args.add(n.value), nil
	}

	if tsquery, ok := ftsFunctions[n.operator]; ok {
		lang := ""
		if n.lang != "" {
			lang = args.add(n.lang) + "::regconfig, "
		}
		// Like PostgREST, anything that isn't a tsvector (text, json, ...)
		// is converted with to_tsvector().
		if col.DataType != "tsvector" || len(n.field.path) > 0 {
			lhs = "to_tsvector(" + lang + lhs + ")"
		}
		return fmt.Sprintf("%s @@ %s(%s%s)", lhs, tsquery, lang, args.add(n.value)), nil
	}

	value := n.value
	switch n.operator {
	case "like", "ilike":
		value = strings.ReplaceAll(value, "*", "%") // PostgREST's URL-friendly wildcard
	case "cs", "cd":
		// This project's older shorthand for array columns: cs.a,b means cs.{a,b}.
		// PostgREST literals ({...}, [...], (...), "...") are passed through as-is.
		if col.DataType == "ARRAY" && value != "" && !strings.ContainsRune(`{[("`, rune(value[0])) {
			value = convertToPostgresArrayOrJSON(value)
		}
	}

	op := sqlOperators[n.operator]
	if n.quant != "" {
		return fmt.Sprintf("%s %s %s(%s)", lhs, op, n.quant, args.add(value)), nil
	}
	return fmt.Sprintf("%s %s %s", lhs, op, args.add(value)), nil
}

// toSQL renders the field. JSON path keys are bind parameters and, like in
// PostgREST, columns that aren't json/jsonb (composites, arrays) go through
// to_jsonb() first.
func (f filterField) toSQL(col schema.Column, args *argList) string {
	sql := quoteIdent(col.Name)
	if len(f.path) == 0 {
		return sql
	}
	if col.DataType != "json" && col.DataType != "jsonb" {
		sql = "to_jsonb(" + sql + ")"
	}
	var b strings.Builder
	b.WriteString("(")
	b.WriteString(sql)
	for _, step := range f.path {
		b.WriteString(step.arrow)
		if step.isIndex {
			b.WriteString(args.add(step.index))
			b.WriteString("::int")
		} else {
			b.WriteString(args.add(step.key))
			b.WriteString("::text")
		}
	}
	b.WriteString(")")
	return b.String()
}

// pgArrayLiteral encodes values as an array literal with every element quoted,
// e.g. {"a","b,c"}. Sent as a text parameter, PostgreSQL parses it as an
// array of whatever type the column has (uuid, int, enum, ...).
func pgArrayLiteral(values []string) string {
	var b strings.Builder
	b.WriteByte('{')
	for i, v := range values {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		for j := 0; j < len(v); j++ {
			if v[j] == '"' || v[j] == '\\' {
				b.WriteByte('\\')
			}
			b.WriteByte(v[j])
		}
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

// argList collects bind parameters.
type argList []any

// add appends v and returns its placeholder ($1, $2, ...).
func (a *argList) add(v any) string {
	*a = append(*a, v)
	return "$" + strconv.Itoa(len(*a))
}

func findColumn(table schema.Table, name string) (schema.Column, bool) {
	for _, col := range table.Columns {
		if col.Name == name {
			return col, true
		}
	}
	return schema.Column{}, false
}

func quoteIdent(name string) string {
	return pgx.Identifier{name}.Sanitize()
}
