-- KEYS[1] = user key (e.g., "sw:user123")
-- ARGV[1] = current timestamp (milliseconds)
-- ARGV[2] = window start (milliseconds)
-- ARGV[3] = limit (max requests per window)
-- ARGV[4] = window length in seconds (for EXPIRE)
-- ARGV[5] = unique member id

local key = KEYS[1]
local now = tonumber(ARGV[1])
local windowStart = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local windowSec = tonumber(ARGV[4])
local member = ARGV[5]

redis.call('ZREMRANGEBYSCORE', key, 0, windowStart)
local count = redis.call('ZCARD', key)
local allowed = 0
local remaining = 0

if count < limit then
    allowed = 1
    remaining = limit - count - 1
    redis.call('ZADD', key, now, member)
    redis.call('EXPIRE', key, windowSec + 1)
end

return {allowed, remaining}
