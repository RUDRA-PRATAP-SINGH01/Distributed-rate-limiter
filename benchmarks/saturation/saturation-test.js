import http from 'k6/http';

// Progressive saturation sweep — reuse throughput-test.js logic.
// Run at finer RPS steps to find max sustainable throughput:
//   k6 run -e TARGET_RPS=1500 benchmarks/saturation/saturation-test.js --out json=benchmarks/saturation/results/1500.json

export const options = {
    scenarios: {
        saturation: {
            executor: 'constant-arrival-rate',
            rate: __ENV.TARGET_RPS,
            timeUnit: '1s',
            duration: '60s',
            preAllocatedVUs: 100,
            maxVUs: 500,
        },
    },
    thresholds: {
        http_req_duration: ['p(99)<100'],
        http_req_failed: ['rate<0.01'],
    },
};

export default function () {
    const userId = `user_${__VU}_${__ITER}`;
    http.get(`http://localhost:9090/check?user_id=${userId}`);
}
