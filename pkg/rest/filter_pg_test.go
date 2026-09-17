package rest

import (
	"context"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/edgeflare/pigo/pkg/pgx/schema"
	"github.com/jackc/pgx/v5"
)

// TestFiltersAgainstPostgres runs filters against a real database
//
//	TEST_DATABASE=postgres://postgres:secret@localhost:5432/testdb go test ./pkg/rest -run Postgres
func TestFiltersAgainstPostgres(t *testing.T) {
	connString := os.Getenv("TEST_DATABASE")
	if connString == "" {
		t.Skip("TEST_DATABASE not set")
	}
	ctx := context.Background()

	setup, err := pgx.Connect(ctx, connString)
	if err != nil {
		t.Fatal(err)
	}
	defer setup.Close(ctx)

	const (
		a = "1ff2974a-bec1-4d3f-9ad4-53709789ad4b"
		b = "444b7f47-7ee7-4cde-b2f9-3d38c6a27cad"
		c = "cd74f06a-d00c-4dde-bc55-5ad2aa6ecf50"
	)
	_, err = setup.Exec(ctx, `
		DROP SCHEMA IF EXISTS pigo_filter_test CASCADE;
		CREATE SCHEMA pigo_filter_test;
		CREATE TYPE pigo_filter_test.mood AS ENUM ('happy', 'sad', 'ok');
		CREATE TYPE pigo_filter_test.coordinates AS (lat numeric, long numeric);
		CREATE TABLE pigo_filter_test.items (
			id int PRIMARY KEY,
			wid uuid,
			name text,
			age int,
			student boolean,
			tags text[],
			nums int[],
			data jsonb,
			body text,
			tsv tsvector GENERATED ALWAYS AS (to_tsvector('french', coalesce(body, ''))) STORED,
			period int4range,
			mood pigo_filter_test.mood,
			location pigo_filter_test.coordinates,
			"information.cpe" text
		);
		INSERT INTO pigo_filter_test.items (id, wid, name, age, student, tags, nums, data, body, period, mood, location, "information.cpe") VALUES
		(1, '`+a+`', 'John',         10, true,  '{a,b}', '{1,2}', '{"blood_type":"A-","age":25,"phones":[{"number":"917"}]}', 'The fat cats', '[1,5)',   'happy', '(19.7,-155.8)', 'xMSx'),
		(2, '`+b+`', 'Hebdon,John',  20, false, '{b,c}', '{3}',   '{"blood_type":"O+","age":30,"phones":[]}', 'amusant chats', '[10,20)', 'sad',   '(10,10)',       'nope'),
		(3, NULL,    'Quote:"',      30, NULL,  '{}',    '{}',    '{"age":15,"a.b":1}', 'dogs', '[5,10)',  'happy', NULL,            NULL),
		(4, '`+c+`', 'O''Brien',     17, true,  '{c}',   '{1}',   '[1,2]', NULL, 'empty',   'ok',    '(5,5)',         NULL),
		(5, '`+a+`', 'Backslash:\',  21, false, '{z}',   '{9}',   '{"age":5}', 'zebra', '[30,40)', 'happy', '(0,0)', 'MS');
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer setup.Exec(ctx, `DROP SCHEMA pigo_filter_test CASCADE`) //nolint:errcheck
	_, trgmErr := setup.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS pg_trgm`)

	// Column types exactly as the schema cache sees them.
	table := schema.Table{Schema: "pigo_filter_test", Name: "items"}
	rows, err := setup.Query(ctx, `SELECT column_name, data_type FROM information_schema.columns
		WHERE table_schema = 'pigo_filter_test' AND table_name = 'items' ORDER BY ordinal_position`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var col schema.Column
		if err := rows.Scan(&col.Name, &col.DataType); err != nil {
			t.Fatal(err)
		}
		table.Columns = append(table.Columns, col)
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}

	tests := []struct {
		query string
		want  []int
	}{
		{"or=(wid.eq." + a + ",wid.eq." + b + ")", []int{1, 2, 5}},
		{"wid=in.(" + a + "," + c + ")", []int{1, 4, 5}},
		{"wid=in.()", nil},
		{"wid=is.null", []int{3}},
		{"wid=not.is.null", []int{1, 2, 4, 5}},
		{"or=(age.lt.15,age.gt.25)", []int{1, 3}},
		{"not.and=(age.gte.15,age.lte.25)", []int{1, 3}},
		{"or=(age.eq.10,not.and(age.gte.11,age.lte.20))", []int{1, 3, 5}},
		{"age=gte.15&age=lte.25", []int{2, 4, 5}},
		{"age=gt(all).{5,15}", []int{2, 3, 4, 5}},
		{"id=in.1,3", []int{1, 3}},
		{`name=in.("Hebdon,John","Quote:\"","Backslash:\\")`, []int{2, 3, 5}},
		{`or=(name.eq."Hebdon,John",name.like.O*)`, []int{2, 4}},
		{"name=eq.O'Brien", []int{4}},
		{"name=like(any).{J*,O*}", []int{1, 4}},
		{"name=like(all).{*o*,*n}", []int{1, 2}},
		{"name=match.^O", []int{4}},
		{"name=imatch.^o", []int{4}},
		{"name=isdistinct.John", []int{2, 3, 4, 5}},
		{"student=is.true", []int{1, 4}},
		{"student=is.unknown", []int{3}},
		{"tags=cs.{b}", []int{1, 2}},
		{"tags=cs.b", []int{1, 2}},
		{"nums=cd.{1,2,3}", []int{1, 2, 3, 4}},
		{"tags=ov.{a,c}", []int{1, 2, 4}},
		{"or=(tags.cs.{a,b},nums.cs.{3})", []int{1, 2}},
		{"data->>blood_type=eq.A-", []int{1}},
		{"data->age=gt.20", []int{1, 2}},
		{"data->phones->0->>number=eq.917", []int{1}},
		{`data=cs.{"blood_type":"O%2B"}`, []int{2}},
		{"data=cs.1", []int{4}},
		{`data->"a.b"=eq.1`, []int{3}},
		{"or=(data->>blood_type.eq.O%2B,data->1.eq.2)", []int{2, 4}},
		{"body=fts(english).cats", []int{1}},
		{"tsv=fts(french).chats", []int{2}},
		{"tsv=plfts(french).dogs", []int{3}},
		{"not.or=(tsv.wfts(french).chats,body.is.null)", []int{1, 3, 5}},
		{"period=ov.[4,6)", []int{1, 3}},
		{"period=sl.[10,11)", []int{1, 3}},
		{`or=(period.adj."[20,30)",period.cs."[2,3]")`, []int{1, 2, 5}},
		{"mood=in.(sad,ok)", []int{2, 4}},
		{"mood=eq.happy&not.or=(id.eq.1,id.eq.3)", []int{5}},
		{"location->lat=gte.19", []int{1}},
		{`%22information.cpe%22=like.*MS*`, []int{1, 5}},
	}
	if trgmErr == nil {
		tests = append(tests, struct {
			query string
			want  []int
		}{"order=similarity(name,'Jon')&limit=1", []int{1}})
	}

	for _, mode := range []pgx.QueryExecMode{pgx.QueryExecModeCacheStatement, pgx.QueryExecModeSimpleProtocol} {
		cfg, err := pgx.ParseConfig(connString)
		if err != nil {
			t.Fatal(err)
		}
		cfg.DefaultQueryExecMode = mode
		conn, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close(ctx)

		for _, tt := range tests {
			t.Run(mode.String()+"/"+tt.query, func(t *testing.T) {
				url := "/pigo_filter_test/items?select=id&" + tt.query
				if !strings.Contains(tt.query, "order=") {
					url += "&order=id"
				}
				sql, args, err := buildSelectQuery(table, parseQueryParams(httptest.NewRequest("GET", url, nil)))
				if err != nil {
					t.Fatal(err)
				}
				rows, err := conn.Query(ctx, sql, args...)
				if err != nil {
					t.Fatalf("%v\nsql: %s\nargs: %#v", err, sql, args)
				}
				got, err := pgx.CollectRows(rows, pgx.RowTo[int])
				if err != nil {
					t.Fatalf("%v\nsql: %s\nargs: %#v", err, sql, args)
				}
				if len(got) == 0 {
					got = nil
				}
				if !reflect.DeepEqual(got, tt.want) {
					t.Errorf("got ids %v, want %v\nsql: %s\nargs: %#v", got, tt.want, sql, args)
				}
			})
		}

		// DELETE with a logic tree removes exactly the rows GET returns.
		t.Run(mode.String()+"/delete", func(t *testing.T) {
			tx, err := conn.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx) //nolint:errcheck
			params := parseQueryParams(httptest.NewRequest("DELETE", "/x?or=(wid.eq."+a+",wid.eq."+b+")", nil))
			sql, args, err := buildDeleteQuery(table, params, nil)
			if err != nil {
				t.Fatal(err)
			}
			tag, err := tx.Exec(ctx, sql, args...)
			if err != nil {
				t.Fatalf("%v\nsql: %s", err, sql)
			}
			if tag.RowsAffected() != 3 {
				t.Errorf("deleted %d rows, want 3", tag.RowsAffected())
			}
		})
	}
}
