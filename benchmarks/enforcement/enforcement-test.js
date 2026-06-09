import http from 'k6/http';

export const options = {
    scenarios: {
        enforce: {
            executor: 'constant-arrival-rate',
            rate: 500,           // 500 req/min
            timeUnit: '1m',
            duration: '60s',
            preAllocatedVUs: 10,
            maxVUs: 20,
        },
    },
};

export default function () {
    const userId = 'single_user';
    http.get(`http://localhost:9090/check?user_id=${userId}`);
}
