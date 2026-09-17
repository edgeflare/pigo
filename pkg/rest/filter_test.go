package rest

import (
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/edgeflare/pigo/pkg/pgx/schema"
)

var filterTestTable = schema.Table{
	Schema: "public",
	Name:   "people",
	Columns: []schema.Column{
		{Name: "id", DataType: "integer"},
		{Name: "wid", DataType: "uuid"},
		{Name: "name", DataType: "text"},
		{Name: "age", DataType: "integer"},
		{Name: "student", DataType: "boolean"},
		{Name: "tags", DataType: "ARRAY"},
		{Name: "data", DataType: "jsonb"},
		{Name: "location", DataType: "USER-DEFINED"},
		{Name: "tsv", DataType: "tsvector"},
		{Name: "period", DataType: "daterange"},
		{Name: "information.cpe", DataType: "text"},
		{Name: "unit price", DataType: "numeric"},
	},
}

func TestBuildWhereClause(t *testing.T) {
	tests := []struct {
		key, value string
		wantSQL    string
		wantArgs   []any
	}{
		// operators
		{"age", "gte.18", `"age" >= $1`, []any{"18"}},
		{"age", "not.eq.2", `NOT ("age" = $1)`, []any{"2"}},
		{"name", "neq.x", `"name" <> $1`, []any{"x"}},
		{"name", "eq.a,b", `"name" = $1`, []any{"a,b"}}, // commas are literal outside logic trees
		{"name", "eq.", `"name" = $1`, []any{""}},
		{"name", "eq.null", `"name" = $1`, []any{"null"}}, // literal string, as in PostgREST
		{"name", "like.*Jo*", `"name" LIKE $1`, []any{"%Jo%"}},
		{"name", "ilike.jo*", `"name" ILIKE $1`, []any{"jo%"}},
		{"name", "match.^J", `"name" ~ $1`, []any{"^J"}},
		{"name", "imatch.^j", `"name" ~* $1`, []any{"^j"}},
		{"name", "isdistinct.x", `"name" IS DISTINCT FROM $1`, []any{"x"}},
		{"name", "like(any).{O*,P*}", `"name" LIKE ANY($1)`, []any{"{O%,P%}"}},
		{"age", "gt(all).{1,2}", `"age" > ALL($1)`, []any{"{1,2}"}},
		{"unit price", "lt.200", `"unit price" < $1`, []any{"200"}},
		{`"information.cpe"`, "like.*MS*", `"information.cpe" LIKE $1`, []any{"%MS%"}},

		// in
		{"id", "in.(1,2,3)", `"id" = ANY($1)`, []any{`{"1","2","3"}`}},
		{"name", `in.("Hebdon,John","Williams,Mary")`, `"name" = ANY($1)`, []any{`{"Hebdon,John","Williams,Mary"}`}},
		{"name", `in.("Quote:\"","Backslash:\\")`, `"name" = ANY($1)`, []any{`{"Quote:\"","Backslash:\\"}`}},
		{"name", `in.("a"b,c)`, `"name" = ANY($1)`, []any{`{"\"a\"b","c"}`}}, // not a complete quoted element
		{"id", "in.()", `"id" = ANY($1)`, []any{`{}`}},
		{"id", "not.in.(1,2)", `NOT ("id" = ANY($1))`, []any{`{"1","2"}`}},
		{"id", "in.1,2", `"id" = ANY($1)`, []any{`{"1","2"}`}}, // legacy unparenthesized form

		// is
		{"wid", "is.null", `"wid" IS NULL`, nil},
		{"wid", "not.is.null", `NOT ("wid" IS NULL)`, nil},
		{"wid", "is.not_null", `"wid" IS NOT NULL`, nil},
		{"student", "is.TRUE", `"student" IS TRUE`, nil},
		{"student", "is.false", `"student" IS FALSE`, nil},
		{"student", "is.unknown", `"student" IS UNKNOWN`, nil},

		// arrays, ranges, jsonb containment
		{"tags", "cs.{a,b}", `"tags" @> $1`, []any{"{a,b}"}},
		{"tags", "cs.a,b", `"tags" @> $1`, []any{`{"a","b"}`}}, // legacy shorthand
		{"tags", "cd.{1,2,3}", `"tags" <@ $1`, []any{"{1,2,3}"}},
		{"tags", "ov.{1,3}", `"tags" && $1`, []any{"{1,3}"}},
		{"data", `cs.{"a":1}`, `"data" @> $1`, []any{`{"a":1}`}},
		{"data", "cs.1", `"data" @> $1`, []any{"1"}},
		{"period", "ov.[2017-01-01,2017-06-30]", `"period" && $1`, []any{"[2017-01-01,2017-06-30]"}},
		{"period", "sl.(1,10)", `"period" << $1`, []any{"(1,10)"}},
		{"period", "sr.(1,10)", `"period" >> $1`, []any{"(1,10)"}},
		{"period", "nxr.(1,10)", `"period" &< $1`, []any{"(1,10)"}},
		{"period", "nxl.(1,10)", `"period" &> $1`, []any{"(1,10)"}},
		{"period", "adj.(1,10)", `"period" -|- $1`, []any{"(1,10)"}},

		// full-text search
		{"tsv", "fts(french).amusant", `"tsv" @@ to_tsquery($1::regconfig, $2)`, []any{"french", "amusant"}},
		{"tsv", "plfts.The Fat Cats", `"tsv" @@ plainto_tsquery($1)`, []any{"The Fat Cats"}},
		{"tsv", "not.phfts(english).The Fat Cats", `NOT ("tsv" @@ phraseto_tsquery($1::regconfig, $2))`, []any{"english", "The Fat Cats"}},
		{"tsv", "wfts.amusant", `"tsv" @@ websearch_to_tsquery($1)`, []any{"amusant"}},
		{"name", "fts(french).amusant", `to_tsvector($1::regconfig, "name") @@ to_tsquery($1::regconfig, $2)`, []any{"french", "amusant"}},
		{"data", "fts.cat", `to_tsvector("data") @@ to_tsquery($1)`, []any{"cat"}},

		// JSON paths
		{"data->>blood_type", "eq.A-", `("data"->>$1::text) = $2`, []any{"blood_type", "A-"}},
		{"data->age", "gt.20", `("data"->$1::text) > $2`, []any{"age", "20"}},
		{"data->phones->0->>number", "eq.1", `("data"->$1::text->$2::int->>$3::text) = $4`, []any{"phones", 0, "number", "1"}},
		{"data->-1", "eq.1", `("data"->$1::int) = $2`, []any{-1, "1"}},
		{"data->2fa", "is.true", `("data"->$1::text) IS TRUE`, []any{"2fa"}},
		{`data->"a.b"`, "eq.1", `("data"->$1::text) = $2`, []any{"a.b", "1"}},
		{"location->lat", "gte.19", `(to_jsonb("location")->$1::text) >= $2`, []any{"lat", "19"}},

		// logic trees
		{"or", "(age.lt.18,age.gt.21)", `("age" < $1 OR "age" > $2)`, []any{"18", "21"}},
		{"and", "(age.gte.11,age.lte.17)", `("age" >= $1 AND "age" <= $2)`, []any{"11", "17"}},
		{"not.and", "(age.gte.0,age.lte.100)", `NOT ("age" >= $1 AND "age" <= $2)`, []any{"0", "100"}},
		{"not.or", "(age.eq.1)", `NOT ("age" = $1)`, []any{"1"}},
		{"or", "(age.eq.14,not.and(age.gte.11,age.lte.17))",
			`("age" = $1 OR NOT ("age" >= $2 AND "age" <= $3))`, []any{"14", "11", "17"}},
		{"or", "(age.not.eq.1,name.is.null)", `(NOT ("age" = $1) OR "name" IS NULL)`, []any{"1"}},
		{"or", "(age.lt.18, age.gt.21)", `("age" < $1 OR "age" > $2)`, []any{"18", "21"}},
		{"or", `(name.eq."Hebdon, John",name.like.*son)`, `("name" = $1 OR "name" LIKE $2)`, []any{"Hebdon, John", "%son"}},
		{"or", `(name.eq."a\"b",name.eq."c)")`, `("name" = $1 OR "name" = $2)`, []any{`a"b`, "c)"}},
		{"or", `(name.eq."unterminated)`, `("name" = $1)`, []any{`"unterminated`}},
		{"or", "(name.eq.a.b@c.com,name.eq.)", `("name" = $1 OR "name" = $2)`, []any{"a.b@c.com", ""}},
		{"or", "(tags.cs.{a,b},id.in.(1,2),wid.is.null)",
			`("tags" @> $1 OR "id" = ANY($2) OR "wid" IS NULL)`, []any{"{a,b}", `{"1","2"}`}},
		{"or", `(period.adj."[18,21)",period.cs."[30,35]")`,
			`("period" -|- $1 OR "period" @> $2)`, []any{"[18,21)", "[30,35]"}},
		{"or", "(data->>a.eq.1,data->0.eq.2)",
			`(("data"->>$1::text) = $2 OR ("data"->$3::int) = $4)`, []any{"a", "1", 0, "2"}},
		{"or", "(tsv.fts(english).cat,name.not.ilike.*x*)",
			`("tsv" @@ to_tsquery($1::regconfig, $2) OR NOT ("name" ILIKE $3))`, []any{"english", "cat", "%x%"}},
		{"or", "(name.like(any).{a*,b*},and(age.gt.1,or(student.is.true,wid.is.null)))",
			`("name" LIKE ANY($1) OR ("age" > $2 AND ("student" IS TRUE OR "wid" IS NULL)))`, []any{"{a%,b%}", "1"}},
	}

	for _, tt := range tests {
		t.Run(tt.key+"="+tt.value, func(t *testing.T) {
			var args argList
			sql, err := buildWhereClause(filterTestTable, []RawFilter{{tt.key, tt.value}}, &args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if sql != tt.wantSQL {
				t.Errorf("sql\n got: %s\nwant: %s", sql, tt.wantSQL)
			}
			if !reflect.DeepEqual([]any(args), tt.wantArgs) {
				t.Errorf("args\n got: %#v\nwant: %#v", []any(args), tt.wantArgs)
			}
		})
	}
}

func TestBuildWhereClauseErrors(t *testing.T) {
	tests := []struct {
		key, value string
		wantErr    string
	}{
		{"age", "gtt.18", `unknown operator "gtt"`},
		{"age", "18", "expected an operator"},
		{"age", "", "expected an operator"},
		{"age", "eq", `expected "."`},
		{"age", "neq(any).{1}", "does not support the any/all modifiers"},
		{"age", "eq(some).{1}", `expected "any)" or "all)"`},
		{"age", "in.(1,2", `expected ")"`},
		{"age", "in.(1,2)x", "unexpected input"},
		{"age", "in.", `expected "("`},
		{"wid", "is.nul", `invalid value "nul" for is`},
		{"wid", "is.nullx", `invalid value "nullx" for is`},
		{"tsv", "fts().x", "expected a text search configuration"},
		{"nope", "eq.1", "column people.nope does not exist"},
		{"", "eq.1", "expected a column name"},
		{"data->", "eq.1", "expected a column name"},
		{"age::text", "eq.1", "unexpected input"}, // casts aren't allowed in filters (PostgREST)
		{"child.name", "eq.x", "filters on embedded resources are not supported"},
		{"child.or", "(a.eq.1)", "filters on embedded resources are not supported"},
		{"or", "(nope.eq.1)", "column people.nope does not exist"},
		{"or", "(a.b.eq.1)", `unknown operator "b"`},
		{"or", "(email.eq.a.b@c.com)", "column people.email does not exist"},
		{"or", "", `expected "("`},
		{"or", "age.eq.1", `expected "("`},
		{"or", "(age.eq.1", `expected "," or ")", found end of input`},
		{"or", "(age.eq.1))", "unexpected input"},
		{"or", "()", "expected a column name"},
		{"or", "(age.eq.1,)", "expected a column name"},
		{"or", "(age.eqq.1)", `unknown operator "eqq"`},
		{"or", "(age.eq)", `expected "."`},
		{"or", "(id.in.1,2)", `expected "("`},
		{"or", "(tags.cs.{a,b}x)", `expected "," or ")"`},
		{"or", "(" + strings.Repeat("or(", 40) + "age.eq.1" + strings.Repeat(")", 41), "nested more than 32 levels"},
	}
	for _, tt := range tests {
		t.Run(tt.key+"="+tt.value, func(t *testing.T) {
			var args argList
			sql, err := buildWhereClause(filterTestTable, []RawFilter{{tt.key, tt.value}}, &args)
			if err == nil {
				t.Fatalf("expected error containing %q, got SQL %q", tt.wantErr, sql)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseQueryParamsFilters(t *testing.T) {
	r := httptest.NewRequest("GET", "/people?select=id&age=lte.65&or=(a.eq.1)&limit=5&age=gte.18&columns=x&on_conflict=id", nil)
	got := parseQueryParams(r).Filters
	want := []RawFilter{{"age", "lte.65"}, {"age", "gte.18"}, {"or", "(a.eq.1)"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("filters\n got: %#v\nwant: %#v", got, want)
	}
}

var tradesTable = schema.Table{
	Schema: "pair_eur_bdt",
	Name:   "trades",
	Columns: []schema.Column{
		{Name: "id", DataType: "uuid"},
		{Name: "taker_wid", DataType: "uuid"},
		{Name: "maker_wid", DataType: "uuid"},
		{Name: "price", DataType: "numeric"},
		{Name: "created_at", DataType: "timestamp with time zone"},
	},
}

func TestBuildSelectQuery(t *testing.T) {
	const wid = "1ff2974a-bec1-4d3f-9ad4-53709789ad4b"
	allCols := `"id"::TEXT as "id", "taker_wid"::TEXT as "taker_wid", "maker_wid"::TEXT as "maker_wid", "price", "created_at"`
	tests := []struct {
		url      string
		wantSQL  string
		wantArgs []any
	}{
		{
			"/pair_eur_bdt/trades?or=(taker_wid.eq." + wid + ",maker_wid.eq." + wid + ")",
			`SELECT ` + allCols + ` FROM "pair_eur_bdt"."trades" WHERE ("taker_wid" = $1 OR "maker_wid" = $2) LIMIT $3`,
			[]any{wid, wid, 100},
		},
		{
			"/pair_eur_bdt/trades?select=*&price=gt.100&or=(taker_wid.eq." + wid + ",maker_wid.eq." + wid + ")&order=created_at.desc&limit=10&offset=20",
			`SELECT ` + allCols + ` FROM "pair_eur_bdt"."trades" WHERE ("taker_wid" = $1 OR "maker_wid" = $2) AND "price" > $3 ORDER BY "created_at" DESC NULLS LAST LIMIT $4 OFFSET $5`,
			[]any{wid, wid, "100", 10, 20},
		},
		{
			"/pair_eur_bdt/trades?select=id,price",
			`SELECT "id"::TEXT as "id", "price" FROM "pair_eur_bdt"."trades" LIMIT $1`,
			[]any{100},
		},
		{
			"/pair_eur_bdt/trades?select=price&id=eq.x&order=similarity(id,'O''Brien')",
			`SELECT "price" FROM "pair_eur_bdt"."trades" WHERE "id" = $1 ORDER BY similarity("id", $2) DESC NULLS LAST LIMIT $3`,
			[]any{"x", "O''Brien", 100},
		},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			sql, args, err := buildSelectQuery(tradesTable, parseQueryParams(httptest.NewRequest("GET", tt.url, nil)))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if sql != tt.wantSQL {
				t.Errorf("sql\n got: %s\nwant: %s", sql, tt.wantSQL)
			}
			if !reflect.DeepEqual(args, tt.wantArgs) {
				t.Errorf("args\n got: %#v\nwant: %#v", args, tt.wantArgs)
			}
		})
	}

	for _, url := range []string{
		"/pair_eur_bdt/trades?order=nope.desc",
		"/pair_eur_bdt/trades?or=(nope.eq.1)",
		"/pair_eur_bdt/trades?price=100",
	} {
		if _, _, err := buildSelectQuery(tradesTable, parseQueryParams(httptest.NewRequest("GET", url, nil))); err == nil {
			t.Errorf("%s: expected an error", url)
		}
	}
}

// PATCH and DELETE share the WHERE builder, so they affect exactly the rows a
// GET with the same filters returns, and a bad filter is never dropped.
func TestMutationsUseSameFilters(t *testing.T) {
	r := httptest.NewRequest("PATCH", "/pair_eur_bdt/trades?or=(taker_wid.eq.a,maker_wid.eq.a)&price=lt.5", nil)
	params := parseQueryParams(r)

	sql, args, err := buildUpdateQuery(tradesTable, map[string]any{"price": 7}, params, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := `UPDATE "pair_eur_bdt"."trades" SET "price" = $1 WHERE ("taker_wid" = $2 OR "maker_wid" = $3) AND "price" < $4`
	if sql != want || !reflect.DeepEqual(args, []any{7, "a", "a", "5"}) {
		t.Errorf("update\n got: %s %#v\nwant: %s", sql, args, want)
	}

	sql, args, err = buildDeleteQuery(tradesTable, params, nil)
	if err != nil {
		t.Fatal(err)
	}
	want = `DELETE FROM "pair_eur_bdt"."trades" WHERE ("taker_wid" = $1 OR "maker_wid" = $2) AND "price" < $3`
	if sql != want || !reflect.DeepEqual(args, []any{"a", "a", "5"}) {
		t.Errorf("delete\n got: %s %#v\nwant: %s", sql, args, want)
	}

	for _, url := range []string{
		"/pair_eur_bdt/trades?nope=eq.1",
		"/pair_eur_bdt/trades?or=(nope.eq.1)",
		"/pair_eur_bdt/trades?id=eqq.1",
	} {
		params := parseQueryParams(httptest.NewRequest("DELETE", url, nil))
		if sql, _, err := buildDeleteQuery(tradesTable, params, nil); err == nil {
			t.Errorf("%s: expected an error, got %q", url, sql)
		}
		if sql, _, err := buildUpdateQuery(tradesTable, map[string]any{"price": 1}, params, nil); err == nil {
			t.Errorf("%s: expected an error, got %q", url, sql)
		}
	}
}

// FuzzParseFilter checks the parser never panics and that successful output
// references exactly the parameters it bound.
func FuzzParseFilter(f *testing.F) {
	for _, seed := range [][2]string{
		{"or", "(age.eq.14,not.and(age.gte.11,age.lte.17))"},
		{"name", `in.("a,b",c)`},
		{"data->a->>0", "fts(english).x"},
		{"or", `(tags.cs.{a,b},name.eq."x\"y")`},
		{"not.and", "(wid.is.null,age.eq(any).{1})"},
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, key, value string) {
		var args argList
		sql, err := buildWhereClause(filterTestTable, []RawFilter{{key, value}}, &args)
		if err != nil {
			return
		}
		for i := 1; i <= len(args); i++ {
			if !strings.Contains(sql, "$"+strconv.Itoa(i)) {
				t.Fatalf("placeholder $%d missing from %q", i, sql)
			}
		}
		if strings.Contains(sql, "$"+strconv.Itoa(len(args)+1)) {
			t.Fatalf("unbound placeholder in %q", sql)
		}
	})
}
