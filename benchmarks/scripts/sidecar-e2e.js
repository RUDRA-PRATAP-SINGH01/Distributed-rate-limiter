import http from 'k6/http';

// Client → Sidecar → Limiter → Redis → Demo upstream
const BASE = __ENV.SIDECAR_URL || 'http://localhost:9090';
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
  const userId = `sc_${__VU}_${__ITER}`;
  http.get(`${BASE}/`, {
    headers: { 'X-User-ID': userId },
    tags: { name: 'sidecar_e2e' },
  });
}
