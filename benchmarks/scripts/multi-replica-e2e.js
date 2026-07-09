import http from 'k6/http';

const A = __ENV.SIDECAR_A || 'http://localhost:9090';
const B = __ENV.SIDECAR_B || 'http://localhost:9092';
const RATE = parseInt(__ENV.TARGET_RPS || '500', 10);
const DURATION = __ENV.DURATION || '60s';

export const options = {
  scenarios: {
    dual: {
      executor: 'constant-arrival-rate',
      rate: RATE,
      timeUnit: '1s',
      duration: DURATION,
      preAllocatedVUs: 50,
      maxVUs: 200,
    },
  },
};

export default function () {
  const base = __ITER % 2 === 0 ? A : B;
  const userId = `mr_${__VU % 10}`; // 10 shared users → quota contention
  http.get(`${base}/?user_id=${userId}`, { tags: { name: 'multi_replica' } });
}
