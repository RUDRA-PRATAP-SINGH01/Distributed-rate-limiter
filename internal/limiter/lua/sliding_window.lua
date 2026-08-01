-- KEYS[1] = user key (e.g., "sw:user123")
-- ARGV[1] = current timestamp (milliseconds)
-- ARGV[2] = window start (milliseconds)
-- ARGV[3] = limit (max requests per window)
-- ARGV[4] = key TTL in seconds (at least 1; derived from window duration)
-- ARGV[5] = unique member id
--
-- Returns {allowed, remaining, retry_after_ms}. This is a sliding log, not a
-- fixed window: there is no shared reset instant, so on denial the wait is the
-- time until the OLDEST in-window entry ages out, never the full window.

local key = KEYS[1]
local now = tonumber(ARGV[1])
local windowStart = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local windowSec = tonumber(ARGV[4])
local member = ARGV[5]

local windowMs = now - windowStart

redis.call('ZREMRANGEBYSCORE', key, 0, windowStart)
local count = redis.call('ZCARD', key)

if count < limit then
    redis.call('ZADD', key, now, member)
    if windowSec < 1 then
        windowSec = 1
    end
    redis.call('EXPIRE', key, windowSec)
    return {1, limit - count - 1, 0}
end

-- ZREMRANGEBYSCORE trims scores <= windowStart inclusively, so a caller that
-- waits retry_after_ms finds this entry already evicted and is admitted.
local retryAfterMs = 0
local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
if oldest[2] then
    retryAfterMs = math.ceil((tonumber(oldest[2]) + windowMs) - now)
    if retryAfterMs < 1 then
        retryAfterMs = 1
    end
end

-- retryAfterMs stays 0 when the set is empty, which only happens on a
-- misconfigured limit <= 0. Callers fall back to their own estimate.
return {0, 0, retryAfterMs}
