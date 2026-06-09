import http from 'k6/http';

export const options = {
    scenarios: {
        hotkey: {
            executor: 'constant-arrival-rate',
            rate: 5000,
            timeUnit: '1s',
            duration: '60s',
            preAllocatedVUs: 100,
            maxVUs: 200,
        },
    },
};

const users = ['alice','bob','charlie','dave','eve','frank','grace','heidi','ivan','judy'];

export default function () {
    const userId = users[Math.floor(Math.random() * users.length)];
    http.get(`http://localhost:9090/check?user_id=${userId}`);
}
