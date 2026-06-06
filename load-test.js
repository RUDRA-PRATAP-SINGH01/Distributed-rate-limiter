import http from 'k6/http';
import { sleep, check } from 'k6';
import { Rate } from 'k6/metrics';

// k6 load test: hits the sidecar (not the central limiter directly) to mimic production traffic.
// real_failures excludes 429 — rate limiting is success, not an outage.

const realFailures = new Rate('real_failures');

export const options = {
    stages: [
        { duration: '10s', target: 50 },
        { duration: '20s', target: 50 },
        { duration: '10s', target: 0 },
    ],
    thresholds: {
        http_req_duration: ['p(99)<20'],
        'real_failures': ['rate<0.01'],   // only real errors (not 429)
    },
};

export default function () {
    const userId = `test_user_${Math.floor(Math.random() * 100)}`;
    const url = `http://localhost:9090/check`;
    // X-User-ID mirrors production: identity from gateway header, not spoofable query param.
    const res = http.get(url, {
        headers: { 'X-User-ID': userId },
    });

    // Real failure: not 200/429, or 5xx, or connection error (status 0)
    const isRealFailure =
        res.status === 0 ||
        res.status >= 500 ||
        (res.status !== 200 && res.status !== 429);
    realFailures.add(isRealFailure);

    // Headers check (case-insensitive lookup)
    const limitHeader = res.headers['X-Ratelimit-Limit'] || res.headers['x-ratelimit-limit'];
    const remainingHeader = res.headers['X-Ratelimit-Remaining'] || res.headers['x-ratelimit-remaining'];
    const hasHeaders = limitHeader !== undefined && remainingHeader !== undefined;

    check(res, {
        'status is 200 or 429': () => res.status === 200 || res.status === 429,
        'has rate limit headers': () => hasHeaders,
    });

    sleep(0.1);
}
