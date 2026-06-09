import http from 'k6/http';

export const options = {
    scenarios: {
        throughput: {
            executor: 'constant-arrival-rate',
            rate: __ENV.TARGET_RPS,
            timeUnit: '1s',
            duration: '60s',
            preAllocatedVUs: 100,
            maxVUs: 500,
        },
    },
    thresholds: {
        http_req_duration: ['p(99)<50'],
        http_req_failed: ['rate<0.01'],
    },
};

export default function () {
    const userId = `user_${__VU}_${__ITER}`;
    http.get(`http://localhost:9090/check?user_id=${userId}`);
}
