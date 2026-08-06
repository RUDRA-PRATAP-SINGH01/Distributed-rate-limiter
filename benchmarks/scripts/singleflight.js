import http from 'k6/http';

// 100 VUs fire simultaneously for the SAME user key — singleflight should collapse limiter calls.
const BASE = __ENV.SIDECAR_URL || 'http://localhost:9090';
const USER = __ENV.SF_USER || 'singleflight-user';

export const options = {
  scenarios: {
    burst: {
      executor: 'shared-iterations',
      vus: 100,
      iterations: 100,
      maxDuration: '30s',
    },
  },
};

export default function () {
  http.get(`${BASE}/`, { headers: { 'X-User-ID': USER }, tags: { name: 'singleflight' } });
}
