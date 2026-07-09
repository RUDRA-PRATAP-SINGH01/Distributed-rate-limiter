import http from 'k6/http';
import { check } from 'k6';

const A = __ENV.SIDECAR_A || 'http://localhost:9090';
const B = __ENV.SIDECAR_B || 'http://localhost:9092';
const KEY = __ENV.IDEMPOTENCY_KEY || 'race-test-key-001';

export const options = {
  scenarios: {
    race: {
      executor: 'shared-iterations',
      vus: 100,
      iterations: 100,
      maxDuration: '30s',
    },
  },
};

export default function () {
  const base = __VU % 2 === 0 ? A : B;
  const payload = JSON.stringify({ bench: true, vu: __VU });
  const res = http.post(`${base}/`, payload, {
    headers: {
      'Content-Type': 'application/json',
      'X-User-Id': 'idem-bench-user',
      'Idempotency-Key': KEY,
    },
    tags: { name: 'idempotency_race' },
  });
  check(res, {
    '200 or 409': (r) => r.status === 200 || r.status === 409,
  });
}
