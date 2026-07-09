import http from 'k6/http';

const BASE = __ENV.LIMITER_URL || 'http://localhost:8080';
const API_KEY = __ENV.INTERNAL_API_KEY || 'dev-internal-key-change-in-prod';
const RATE = parseInt(__ENV.TARGET_RPS || '1000', 10);
const DURATION = __ENV.DURATION || '60s';
const WARMUP = __ENV.WARMUP || '10s';

export const options = {
  scenarios: {
    warmup: {
      executor: 'constant-arrival-rate',
      rate: Math.min(RATE, 100),
      timeUnit: '1s',
      duration: WARMUP,
      preAllocatedVUs: 20,
      maxVUs: 100,
      startTime: '0s',
      exec: 'hit',
    },
    measure: {
      executor: 'constant-arrival-rate',
      rate: RATE,
      timeUnit: '1s',
      duration: DURATION,
      preAllocatedVUs: 100,
      maxVUs: 500,
      startTime: WARMUP,
      exec: 'hit',
    },
  },
};

export function hit() {
  const userId = `hier_${__VU}_${__ITER}`;
  const endpoint = `/api/bench/${__VU % 10}`;
  http.get(`${BASE}/check_hierarchical?endpoint=${encodeURIComponent(endpoint)}`, {
    headers: {
      'X-Internal-API-Key': API_KEY,
      'X-User-Id': userId,
      'X-Tenant-Id': `tenant_${__VU % 5}`,
    },
    tags: { name: 'hierarchical_check' },
  });
}
