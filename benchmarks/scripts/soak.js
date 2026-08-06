import http from 'k6/http';

const BASE = __ENV.SIDECAR_URL || 'http://localhost:9090';
const RATE = parseInt(__ENV.TARGET_RPS || '500', 10);
const DURATION = __ENV.DURATION || '15m';

export const options = {
  scenarios: {
    soak: {
      executor: 'constant-arrival-rate',
      rate: RATE,
      timeUnit: '1s',
      duration: DURATION,
      preAllocatedVUs: 50,
      maxVUs: 150,
    },
  },
};

export default function () {
  const userId = `soak_${__VU}_${__ITER}`;
  http.get(`${BASE}/`, { headers: { 'X-User-ID': userId }, tags: { name: 'soak' } });
}
