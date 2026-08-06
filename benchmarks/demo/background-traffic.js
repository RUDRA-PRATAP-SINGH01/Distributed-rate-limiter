import http from 'k6/http';

export const options = {
    scenarios: {
        background: {
            executor: 'constant-arrival-rate',
            rate: 15,
            timeUnit: '1s',
            duration: '24h',
            preAllocatedVUs: 5,
            maxVUs: 15,
        },
    },
};

const users = ['bg_user_1', 'bg_user_2', 'bg_user_3', 'bg_user_4', 'bg_user_5'];

export default function () {
    const userId = users[Math.floor(Math.random() * users.length)];
    // Hit the sidecar proxy root — rate-limited, then forwarded to demo-backend.
    http.get('http://localhost:9090/', { headers: { 'X-User-ID': userId } });
}
