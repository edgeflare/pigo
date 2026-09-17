// Package rest provides a PostgreSQL REST API server compatible with PostgREST.
//
// The server automatically exposes database tables and views as REST endpoints.
// Each endpoint supports standard HTTP methods: GET, POST, PATCH, DELETE.
//
// All endpoints live under the base path passed to NewServer (default /api).
// Tables and views are exposed at <basePath>/table_or_view_name or
// <basePath>/schema_name/table_or_view_name; schema_name defaults to public.
// Functions are exposed at <basePath>/rpc (see Functions below).
//
// # Query parameters
//
// Reserved query parameters control the selected columns, ordering and pagination:
//
//	Parameter                              | Description
//	---------------------------------------|--------------------------------------------------
//	?select=col1,col2                      | Select specific columns (default: *, all columns)
//	?order=col.desc,col2                   | Order results (asc/desc, nullsfirst/nullslast)
//	?order=similarity(col,'search string') | Order by similarity (requires pg_trgm extension)
//	?limit=100                             | Limit number of results (default: 100)
//	?offset=0                              | Pagination offset (default: 0)
//
// columns and on_conflict are reserved too. Every other query parameter is a filter.
//
// # Filtering
//
// A filter has the form ?column=[not.]operator.value. All filters are combined
// with AND, including repeated keys (?age=gte.18&age=lte.65), and GET, PATCH and
// DELETE apply them identically.
//
//	Operator    | SQL                       | Example
//	------------|---------------------------|-----------------------------------------
//	eq          | =                         | ?id=eq.5
//	neq         | <>                        | ?status=neq.done
//	gt, gte     | >, >=                     | ?age=gte.18
//	lt, lte     | <, <=                     | ?price=lt.100
//	like, ilike | LIKE, ILIKE               | ?name=ilike.*john*  (* is an alias for %)
//	match       | ~                         | ?name=match.^J
//	imatch      | ~*                        | ?name=imatch.^j
//	in          | = ANY(...)                | ?id=in.(1,2,3)
//	is          | IS                        | ?col=is.null  (or not_null, true, false, unknown)
//	isdistinct  | IS DISTINCT FROM          | ?col=isdistinct.x
//	cs, cd      | @>, <@                    | ?tags=cs.{a,b}  ?data=cs.{"key":"value"}
//	ov          | &&                        | ?tags=ov.{a,b}  ?period=ov.[2017-01-01,2017-06-30]
//	sl, sr      | <<, >>                    | ?range=sl.(1,10)
//	nxr, nxl    | &<, &>                    | ?range=nxr.(1,10)
//	adj         | -|-                       | ?range=adj.(1,10)
//	fts         | @@ to_tsquery             | ?tsv=fts(english).cats
//	plfts       | @@ plainto_tsquery        | ?tsv=plfts.fat%20cats
//	phfts       | @@ phraseto_tsquery       | ?tsv=phfts(english).fat%20cats
//	wfts        | @@ websearch_to_tsquery   | ?tsv=wfts.fat%20-dogs
//
// Prefix an operator with not. to negate it: ?id=not.in.(1,2). The any and all
// modifiers apply to eq, gt, gte, lt, lte, like, ilike, match and imatch:
// ?name=like(any).{O*,P*}. The fts operators take an optional text search
// configuration, fts(english), and columns that aren't tsvector (text, json)
// are converted with to_tsvector(). eq.null compares with the string 'null';
// use is.null to match NULL.
//
// The logical operators and / or combine conditions, and can be negated with
// not. and nested:
//
//	?or=(age.lt.18,age.gt.21)
//	?not.and=(age.gte.0,age.lte.100)
//	?grade=gte.90&or=(age.eq.14,not.and(age.gte.11,age.lte.17))
//
// A plain column filter takes its value verbatim (?name=eq.a,b matches "a,b").
// Inside and/or and in lists, values containing reserved characters (commas,
// dots, colons, parentheses) must be double-quoted, with \" and \\ as escapes:
//
//	?or=(name.eq."Doe, John",period.adj."[18,21)")
//	?name=in.("Hebdon,John","Quote:\"")
//
// Column names can be quoted the same way (?"information.cpe"=like.*MS*), and
// json/jsonb columns can be filtered by path with -> and ->> (composite and
// array columns are converted with to_jsonb() first):
//
//	?data->>blood_type=eq.A-
//	?data->phones->0->>number=eq.917
//
// Malformed filters and unknown filter or order columns are rejected with
// 400 Bad Request; a filter is never silently ignored. Casts in filters and
// filters on embedded resources (?child.col=eq.x) are not supported.
//
// For backwards compatibility, ?col=in.a,b,c and, on array columns,
// ?col=cs.a,b and ?col=cd.a,b are accepted as shorthands for the parenthesized
// and braced forms above.
//
// # Functions
//
// PostgreSQL functions are exposed at <basePath>/rpc/function_name or
// <basePath>/rpc/schema_name/function_name; schema_name defaults to public.
// With the default base path:
//
//	POST /api/rpc/add_them          {"a": 1, "b": 2}
//	POST /api/rpc/billing/invoice   {"customer_id": 42}
//
// Functions are called with POST and a JSON object of arguments keyed by
// parameter name (arg1, arg2, ... for unnamed parameters); the body may be
// empty for functions without parameters. Set-returning functions respond with
// an array of rows, other functions with their result value. Only plain
// functions are exposed, not procedures or aggregates.
//
// # Headers
//
// The Prefer header controls the response:
//
//	Header                         | Description
//	-------------------------------|----------------------------------------
//	Prefer: return=minimal         | POST/PATCH/DELETE: return status code only (default)
//	Prefer: return=representation  | POST/PATCH/DELETE: return modified rows in response body
//	Prefer: return=headers-only    | POST/PATCH/DELETE: return headers with metadata only
//	Prefer: count=exact            | Add exact total count of rows for the query in Content-Range header
//
// API is compatible with PostgREST. For more details, see:
// https://docs.postgrest.org/en/stable/references/api/tables_views.html
//
// Example usage:
//
//	server, err := rest.NewServer("postgres://user:pass@localhost/db", "/api")
//	if err != nil {
//		log.Fatal(err)
//	}
//	defer server.Shutdown(context.Background())
//	log.Fatal(server.Start(":8080"))
package rest
