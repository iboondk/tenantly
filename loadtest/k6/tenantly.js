// k6 load test for the Tenantly API.
//
// Usage (from the module root):
//   BASE_URL=http://localhost:8080 VUS=10 DURATION=1m k6 run loadtest/k6/tenantly.js
//
// setup() creates one dev user, one tenant and five projects per VU. Every
// iteration runs a weighted mix of REST calls, a GraphQL query every fifth
// iteration, and one cross-tenant GET that must be denied with 404.
//
// The server must run with APP_ENV=dev or APP_ENV=test so that
// POST /v1/auth/dev-token exists.
//
// Rate limit: the API rate-limits every request by user and tenant (see
// internal/ratelimit, KeyByTenantUser). The server default is
// RATE_LIMIT_PER_MINUTE=120, about 2 requests per second per key. This script
// is a closed loop with no sleep, so each VU sends far more than that. With the
// default server setting most requests return 429 and the checks threshold
// fails. Choose one of these before the run:
//   - measure throughput: start the API with RATE_LIMIT_PER_MINUTE high enough
//     for the VU count (for example 1000000), and record that value in
//     loadtest/RESULTS.md;
//   - keep the default: expect 429s, which show up in the rate_limited column.
// The cross-tenant GET runs behind the same limiter, so it can also return 429.
// A 429 there is not a leak, but the denial was not verified, so its check fails.
//
// Optional: SLEEP=<seconds> (default 0) pauses each VU at the end of every
// iteration to pace the load below the rate limit.

import http from 'k6/http';
import { check, fail, sleep } from 'k6';
import { Rate } from 'k6/metrics';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const VUS = parseInt(__ENV.VUS || '10', 10);
const DURATION = __ENV.DURATION || '30s';
const SLEEP = parseFloat(__ENV.SLEEP || '0');
const PROJECTS_PER_TENANT = 5;

// Endpoint names used as the `name` tag. Every metric the report reads is tagged with one of these.
const EP = {
  list: 'list_projects',
  get: 'get_project',
  create: 'create_project',
  graphql: 'graphql_projects',
  crossTenant: 'cross_tenant_get',
};
const ENDPOINTS = Object.values(EP);

// Share of responses with status 429, per endpoint. Exported as metric rate_limited.
const rateLimited = new Rate('rate_limited');
// Share of cross-tenant reads that did not return 404. Any value above zero is a data leak.
const crossTenantLeak = new Rate('cross_tenant_leak');

// Submetrics only appear in the summary export when a threshold references them.
// The per-endpoint entries below use always-true conditions so that the
// per-endpoint rows exist in summary.json. The real gates are the global
// thresholds and the cross-tenant check.
const endpointVisibility = {};
for (const name of ENDPOINTS) {
  endpointVisibility[`http_reqs{name:${name}}`] = ['count>=0'];
  endpointVisibility[`http_req_duration{name:${name}}`] = ['p(95)>=0'];
  endpointVisibility[`http_req_failed{name:${name}}`] = ['rate>=0'];
  endpointVisibility[`rate_limited{name:${name}}`] = ['rate>=0'];
}

export const options = {
  scenarios: {
    mixed: {
      executor: 'constant-vus',
      vus: VUS,
      duration: DURATION,
    },
  },
  // p(50) and p(99) are not in k6's default summary stats, so they are listed here.
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(50)', 'p(90)', 'p(95)', 'p(99)'],
  thresholds: Object.assign(
    {
      // expected_response:true only covers requests whose status was expected, so it cannot
      // trip on its own. The global http_req_failed gate below catches unexpected errors and 429s.
      'http_req_failed{expected_response:true}': ['rate<0.01'],
      http_req_failed: ['rate<0.01'],
      http_req_duration: ['p(95)<300'],
      checks: ['rate>0.99'],
      // Any cross-tenant read that is not 404 is a data leak, so the gate is zero tolerance.
      cross_tenant_leak: ['rate==0'],
    },
    endpointVisibility,
  ),
};

function authHeaders(token) {
  return { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' };
}

// Adds the endpoint name tag to a request's params. Callers also pass
// responseCallback: http.expectedStatuses(...), so that the expected status
// (for example 404 on the cross-tenant read) does not count as a failure.
function tagged(name, extra) {
  return Object.assign({ tags: { name } }, extra || {});
}

export function setup() {
  if (VUS < 2) {
    fail('VUS must be at least 2: the cross-tenant check needs a second tenant');
  }
  const runId = Date.now().toString(36);
  const users = [];
  for (let i = 0; i < VUS; i++) {
    const email = `loadtest+${i}@example.test`;
    const login = http.post(
      `${BASE_URL}/v1/auth/dev-token`,
      JSON.stringify({ email }),
      tagged('setup_dev_token', { headers: { 'Content-Type': 'application/json' } }),
    );
    if (login.status !== 200) {
      fail(`setup: dev-token for VU ${i} returned ${login.status}`);
    }
    const token = login.json('token');

    const tenant = http.post(
      `${BASE_URL}/v1/tenants`,
      JSON.stringify({ name: `lt-${runId}-${i}` }),
      tagged('setup_create_tenant', { headers: authHeaders(token) }),
    );
    if (tenant.status !== 201) {
      fail(`setup: create tenant for VU ${i} returned ${tenant.status}`);
    }
    const tenantId = tenant.json('id');

    const projectIds = [];
    for (let p = 0; p < PROJECTS_PER_TENANT; p++) {
      const proj = http.post(
        `${BASE_URL}/v1/tenants/${tenantId}/projects`,
        JSON.stringify({ name: `seed-${runId}-${p}` }),
        tagged('setup_create_project', { headers: authHeaders(token) }),
      );
      if (proj.status !== 201) {
        fail(`setup: seed project for VU ${i} returned ${proj.status}`);
      }
      projectIds.push(proj.json('id'));
    }
    users.push({ token, tenantId, projectIds });
  }
  return { users };
}

export default function (data) {
  const idx = __VU - 1;
  const me = data.users[idx];
  const other = data.users[(idx + 1) % data.users.length];
  const iter = __ITER;

  // REST mix: 70% list, 20% get single project, 10% create project.
  const roll = Math.random();
  if (roll < 0.7) {
    const res = http.get(
      `${BASE_URL}/v1/tenants/${me.tenantId}/projects`,
      tagged(EP.list, { headers: authHeaders(me.token), responseCallback: http.expectedStatuses(200) }),
    );
    rateLimited.add(res.status === 429, { name: EP.list });
    check(res, { 'list projects 200': (r) => r.status === 200 }, { name: EP.list });
  } else if (roll < 0.9) {
    const pid = me.projectIds[Math.floor(Math.random() * me.projectIds.length)];
    const res = http.get(
      `${BASE_URL}/v1/tenants/${me.tenantId}/projects/${pid}`,
      tagged(EP.get, { headers: authHeaders(me.token), responseCallback: http.expectedStatuses(200) }),
    );
    rateLimited.add(res.status === 429, { name: EP.get });
    check(res, { 'get project 200': (r) => r.status === 200 }, { name: EP.get });
  } else {
    const name = `p-${__VU}-${iter}-${Math.random().toString(36).slice(2, 10)}`;
    const res = http.post(
      `${BASE_URL}/v1/tenants/${me.tenantId}/projects`,
      JSON.stringify({ name }),
      tagged(EP.create, { headers: authHeaders(me.token), responseCallback: http.expectedStatuses(201) }),
    );
    rateLimited.add(res.status === 429, { name: EP.create });
    check(res, { 'create project 201': (r) => r.status === 201 }, { name: EP.create });
  }

  // GraphQL projects query every fifth iteration.
  if (iter % 5 === 0) {
    const gql = JSON.stringify({
      query: 'query($t: UUID!) { projects(tenantId: $t, limit: 10) { id name } }',
      variables: { t: me.tenantId },
    });
    const res = http.post(
      `${BASE_URL}/graphql`,
      gql,
      tagged(EP.graphql, { headers: authHeaders(me.token), responseCallback: http.expectedStatuses(200) }),
    );
    rateLimited.add(res.status === 429, { name: EP.graphql });
    // gqlgen returns HTTP 200 with an `errors` array for resolver, authz or complexity failures,
    // so the status alone is not enough.
    check(
      res,
      {
        'graphql projects 200': (r) => {
          if (r.status !== 200) return false;
          let body;
          try {
            body = r.json();
          } catch (e) {
            return false; // a 200 with a non-JSON body is a failure
          }
          return !body.errors && !!body.data && Array.isArray(body.data.projects);
        },
      },
      { name: EP.graphql },
    );
  }

  // Cross-tenant GET: this user asks for the other tenant's project list. Expect 404.
  const denied = http.get(
    `${BASE_URL}/v1/tenants/${other.tenantId}/projects`,
    tagged(EP.crossTenant, { headers: authHeaders(me.token), responseCallback: http.expectedStatuses(404) }),
  );
  rateLimited.add(denied.status === 429, { name: EP.crossTenant });
  // A 2xx or 3xx answer means another tenant's data was served. A 429 or error is not a leak.
  crossTenantLeak.add(denied.status >= 200 && denied.status < 400);
  check(denied, { 'cross-tenant denied': (r) => r.status === 404 }, { name: EP.crossTenant });

  if (SLEEP > 0) {
    sleep(SLEEP);
  }
}

// Writes the JSON summary to loadtest/results/summary.json and a short text summary to stdout.
export function handleSummary(data) {
  const m = data.metrics || {};
  const val = (key, field) => {
    const metric = m[key];
    if (!metric || !metric.values || metric.values[field] === undefined) return 'n/a';
    return String(Number(metric.values[field]).toFixed(2));
  };
  const lines = [
    '',
    'Tenantly k6 run (see loadtest/RESULTS.md for the recorded table)',
    `  requests:        ${val('http_reqs', 'count')}`,
    `  requests/s:      ${val('http_reqs', 'rate')}`,
    `  p95 ms:          ${val('http_req_duration', 'p(95)')}`,
    `  p99 ms:          ${val('http_req_duration', 'p(99)')}`,
    `  failed rate:     ${val('http_req_failed', 'rate')}`,
    `  checks pass:     ${val('checks', 'rate')}`,
    '',
  ];
  return {
    'loadtest/results/summary.json': JSON.stringify(data),
    stdout: lines.join('\n'),
  };
}
