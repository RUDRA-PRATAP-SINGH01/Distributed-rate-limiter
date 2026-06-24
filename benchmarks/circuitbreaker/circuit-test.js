import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter, Rate } from 'k6/metrics';

const circuitOpen = new Counter('circuit_open_responses');
const circuitRejected = new Rate('circuit_rejected_rate');

export const options = {
  scenarios: {
    steady: {
      executor: 'constant-vus',
      vus: 20,
      duration: '30s',
    },
    spike_failures: {
      executor: 'constant-vus',
      vus: 50,
      duration: '20s',
      startTime: '35s',
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.5'],
    circuit_rejected_rate: ['rate<0.9'],
  },
};

const BASE = __ENV.SIDECAR_URL || 'http://localhost:9090';
const USER = __ENV.USER_ID || 'circuit-bench-user';

export default function () {
  const res = http.get(`${BASE}/api/test`, {
    headers: { 'X-User-ID': USER },
  });

  if (res.status === 503 && res.body && res.body.includes('circuit')) {
    circuitOpen.add(1);
    circuitRejected.add(true);
  } else {
    circuitRejected.add(false);
  }

  check(res, {
    'status is 200 or 429 or 503': (r) => [200, 429, 503].includes(r.status),
  });
  sleep(0.05);
}
