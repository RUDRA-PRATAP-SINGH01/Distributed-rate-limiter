import http from 'k6/http';
import { check } from 'k6';

// Phase 1: one request per VU to exhaust quota (cache miss path).
// Phase 2: hammer same user — denial-cache hit path.
const BASE = __ENV.SIDECAR_URL || 'http://localhost:9090';
const USER = __ENV.DENIAL_USER || 'denial-cache-user';

export const options = {
  scenarios: {
    prime: {
      executor: 'per-vu-iterations',
      vus: 15,
      iterations: 1,
      maxDuration: '30s',
      exec: 'prime',
    },
    hammer: {
      executor: 'constant-vus',
      vus: 50,
      duration: '30s',
      startTime: '5s',
      exec: 'hammer',
    },
  },
};

export function prime() {
  http.get(`${BASE}/?user_id=${USER}`, { tags: { phase: 'prime' } });
}

export function hammer() {
  const res = http.get(`${BASE}/?user_id=${USER}`, { tags: { phase: 'hammer' } });
  check(res, {
    'denied or allowed': (r) => r.status === 200 || r.status === 429,
  });
}
