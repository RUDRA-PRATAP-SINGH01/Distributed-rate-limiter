-- KEYS[1] = user key (e.g., "rate:user123")
-- ARGV[1] = capacity
-- ARGV[2] = refill_rate (tokens per second)
-- ARGV[3] = current timestamp (milliseconds)
-- ARGV[4] = requested tokens (always 1)

local key = KEYS[1]
local capacity = tonumber(ARGV[1])
local refill_rate = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local requested = tonumber(ARGV[4])

-- Get current bucket state
local bucket = redis.call('HMGET', key, 'tokens', 'last_refill')
local tokens = tonumber(bucket[1])
local last_refill = tonumber(bucket[2])

-- First time user
if tokens == nil then
    tokens = capacity
    last_refill = now
end

-- Refill based on elapsed time (in ms)
local elapsed = (now - last_refill) / 1000.0
if elapsed < 0 then
    elapsed = 0
end
local new_tokens = tokens + (elapsed * refill_rate)
if new_tokens > capacity then
    new_tokens = capacity
end

-- Check if allowed
local allowed = 0
local remaining = math.floor(new_tokens)
if remaining >= requested then
    new_tokens = new_tokens - requested
    allowed = 1
    remaining = math.floor(new_tokens)
end

-- Write back
redis.call('HMSET', key, 'tokens', new_tokens, 'last_refill', now)
redis.call('EXPIRE', key, 3600)

return {allowed, remaining}
