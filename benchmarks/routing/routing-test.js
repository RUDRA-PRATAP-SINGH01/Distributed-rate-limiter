import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter } from 'k6/metrics';

const gatewayA = new Counter('gateway_a');
const gatewayB = new Counter('gateway_b');
const gatewayC = new Counter('gateway_c');
const failovers = new Counter('failovers');

export const options = {
  vus: 20,
  duration: '60s',
};

const BASE = __ENV.SIDECAR_URL || 'http://localhost:9090';

export default function () {
  const res = http.post(`${BASE}/api/payments`, JSON.stringify({ amount: 100 }), {
    headers: {
      'Content-Type': 'application/json',
      'X-User-ID': 'routing-user',
    },
  });

  check(res, {
    'status 200': (r) => r.status === 200,
    'has gateway header': (r) => r.headers['X-Gateway-Id'] !== undefined,
  });

  const gw = res.headers['X-Gateway-Id'];
  if (gw === 'gateway-a') gatewayA.add(1);
  if (gw === 'gateway-b') gatewayB.add(1);
  if (gw === 'gateway-c') gatewayC.add(1);
  if (res.headers['X-Gateway-Failover'] === 'true') failovers.add(1);

  sleep(0.05);
}
