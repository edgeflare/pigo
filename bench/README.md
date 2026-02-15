# PIGO benchmarks

We're comparing against the popular https://github.com/PostgREST/postgrest.

0. Adjust the IP address of Dex to the LAN IP or equivalent in
- docker-compose.yaml's example-app from which we obtain JWT token issued by the Dex IdP
- dex-config.yaml
- pigo-config.yaml

1. Start all containers

```sh
docker compose up -d
```

1. Create JWT secret for PostgREST

```sh
curl http://192.168.0.10:5556/dex/keys | jq '.keys[0]' > dex-jwks.json
```

2. Create needed tables, functions etc from psql console

```sh
PGHOST=localhost PGUSER=postgres PGPASSWORD=postgrespw PGDATABASE=main psql
```
- execute [001_iam.sql](./001_iam.sql) and [002_transactions.sql](./002_transactions.sql)


4. Once the SQL is executed successfully in the above step, restart all containers to reload schema cache etc

```sh
docker compose down
docker compose up -d
```

5. Visit the example-app at [http://localhost:5555](http://localhost:5555). It should present 2 login options,
each with 1 test user. *Login with Example* doesn't require username/password; use `admin@example.com:password` with email login.
This triggers insertion of 2 rows in public.refresh_token in the `dex` database. PIGO pipelines syncs the users in `iam.users` table
in the `main` (your appliaction database). We now can reference `iam.users(sub)` from in table eg in `public.transactions`.

6. Insert the [003_mocks.sql](./003_mocks.sql) data into the `transactions` table using psql

7. Test the setup
```sh
export TOKEN=eyJhbGciOiJS....
```

PostgREST
```sh
curl "localhost:3000/transactions?select=id,user_id" -H "authorization: Bearer $TOKEN"
```

pigo rest
```sh
curl "localhost:8001/transactions?select=id,user_id" -H "authorization: Bearer $TOKEN"
```

Notice only the rows satisfiying `user_id == iam.user_jwt_sub()` are returned

8. Benchmark with [k6](https://github.com/grafana/k6)


pigo rest

```sh
export BASE_URL="http://localhost:8001/transactions?select=id,user_id"
export VUS=10000
k6 run bench/script.js
```

PostGREST

```sh
export BASE_URL="http://localhost:3000/transactions?select=id,user_id"
export VUS=1000 # can't comfortably handle more 1k+ virutal users
k6 run bench/script.js
```
