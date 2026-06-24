import http from 'k6/http';
import { check, sleep } from 'k6';

// Replay test: seed a completed key, then hammer replays (no upstream execution).

export const options = {
  vus: 50,
  duration: '30s',
};

const BASE = __ENV.SIDECAR_URL || 'http://localhost:9090';
const KEY = __ENV.IDEMPOTENCY_KEY || 'replay-test-key-001';

export function setup() {
  const payload = JSON.stringify({ amount: 500, currency: 'INR' });
  const res = http.post(`${BASE}/api/orders`, payload, {
    headers: {
      'Content-Type': 'application/json',
      'X-User-ID': 'idem-user-replay',
      'Idempotency-Key': KEY,
    },
  });
  return { seeded: res.status === 201 };
}

export default function (data) {
  if (!data.seeded) {
    return;
  }
  const payload = JSON.stringify({ amount: 500, currency: 'INR' });
  const res = http.post(`${BASE}/api/orders`, payload, {
    headers: {
      'Content-Type': 'application/json',
      'X-User-ID': 'idem-user-replay',
      'Idempotency-Key': KEY,
    },
  });

  check(res, {
    'replay status 201': (r) => r.status === 201,
    'replayed header': (r) => r.headers['X-Idempotency-Status'] === 'replayed',
  });
  sleep(0.05);
}
