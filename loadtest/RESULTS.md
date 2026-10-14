# Load test results

> **Template. Every value below is `TBD`.** Results must come from a real k6 run against a running Tenantly API. Numbers are never estimated, copied from another run, or written by hand. Replace a `TBD` only with output from `make k6-report`, and record the metadata of that run.

## Run metadata

| Field | Value |
|---|---|
| Date | TBD |
| Commit | TBD |
| Machine | TBD |
| VUS | TBD |
| Duration | TBD |
| Rate limit per minute (`RATE_LIMIT_PER_MINUTE` on the API) | TBD |
| SLEEP between iterations (k6 `SLEEP`, default 0) | TBD |
| Postgres version | TBD |
| Redis version | TBD |

## Results

Columns: request count, requests per second, latency percentiles of `http_req_duration` in ms, error percentage (`http_req_failed`, which includes 429 responses), and the share of 429 responses.

| Endpoint | Requests | RPS | p50 ms | p95 ms | p99 ms | Error % | 429 % |
|---|---|---|---|---|---|---|---|
| list_projects | TBD | TBD | TBD | TBD | TBD | TBD | TBD |
| get_project | TBD | TBD | TBD | TBD | TBD | TBD | TBD |
| create_project | TBD | TBD | TBD | TBD | TBD | TBD | TBD |
| graphql_projects | TBD | TBD | TBD | TBD | TBD | TBD | TBD |
| cross_tenant_get | TBD | TBD | TBD | TBD | TBD | TBD | TBD |
| All endpoints | TBD | TBD | TBD | TBD | TBD | TBD | TBD |

`All endpoints` uses the global metrics. These include the requests that `setup()` sends, so compare it with the endpoint rows only after noting that difference.

## How to run

1. Start the API with `APP_ENV=dev` (or `test`), so that `POST /v1/auth/dev-token` is served. Use `.env.example` as the starting point and replace every placeholder.
   
   **Rate limit.** `.env.example` sets `RATE_LIMIT_PER_MINUTE=120` (about 2 requests per second per user and tenant). The k6 script is a closed loop with no sleep, so with that default most requests return 429 and the `checks rate>0.99` threshold fails. Pick one before running:
   - To measure throughput, set `RATE_LIMIT_PER_MINUTE` high enough for the VU count (for example `1000000`) and record the value in the metadata table.
   - To keep the default, expect 429s. They are counted in the `429 %` column and fail the checks threshold.
   - To pace the load under the limit, run k6 with `SLEEP=<seconds>`, and record that value.
2. Install k6 and make sure it is on `PATH`.
3. Run the load test from the module root:

   ```sh
   make k6
   ```

   Optional environment variables: `BASE_URL` (default `http://localhost:8080`), `VUS` and `DURATION`. The run writes `loadtest/results/summary.json`, which is not committed.

4. Print the table from the raw summary:

   ```sh
   make k6-report
   ```

5. Copy the rows from that output into the results table above, and fill in the metadata table. Keep every value that the report does not contain as `TBD`, or write `n/a`.
