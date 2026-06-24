import http from 'k6/http';
import { check, sleep } from 'k6';

// 100 virtual users fire one request each with the SAME Idempotency-Key.
// Expect: exactly 1 upstream execution, 99 conflicts or replays after completion.

export const options = {
  scenarios: {
    race: {
      executor: 'shared-iterations',
      vus: 100,
      iterations: 100,
      maxDuration: '30s',
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.99'],
  },
};

const BASE = __ENV.SIDECAR_URL || 'http://localhost:9090';
const KEY = __ENV.IDEMPOTENCY_KEY || 'race-test-key-001';

export default function () {
  const payload = JSON.stringify({ amount: 1000, currency: 'INR' });
  const res = http.post(`${BASE}/api/orders`, payload, {
    headers: {
      'Content-Type': 'application/json',
      'X-User-ID': 'idem-user-1',
      'Idempotency-Key': KEY,
    },
  });

  check(res, {
    'status is 201 or 409': (r) => r.status === 201 || r.status === 409,
    'has idempotency header': (r) => r.headers['X-Idempotency-Status'] !== undefined,
  });

  sleep(0.1);
}

export function handleSummary(data) {
  return {
    stdout: JSON.stringify(data, null, 2),
  };
}
