-- KEYS[1..4]: global_key, tenant_key, user_key, endpoint_key
-- ARGV[1..4]: capacities (global, tenant, user, endpoint)
-- ARGV[5..8]: refill_rates (global, tenant, user, endpoint)
-- ARGV[9]: current timestamp (Unix seconds)
-- ARGV[10]: requested tokens (always 1)

local levels = 4
local allowed = 1
local min_remaining = math.huge

-- Step 1: Refill and check each level
for i = 1, levels do
    local key = KEYS[i]
    local capacity = tonumber(ARGV[i])
    local refill_rate = tonumber(ARGV[4 + i])
    local now = tonumber(ARGV[9])

    -- Read bucket state
    local bucket = redis.call('HMGET', key, 'tokens', 'last_refill')
    local tokens = tonumber(bucket[1])
    local last_refill = tonumber(bucket[2])

    if tokens == nil then
        tokens = capacity
        last_refill = now
    end

    -- Refill based on elapsed time
    local elapsed = now - last_refill
    local new_tokens = tokens + elapsed * refill_rate
    if new_tokens > capacity then
        new_tokens = capacity
    end

    -- Check if this level can allow the request
    if new_tokens < 1 then
        allowed = 0
    end

    -- Track the smallest remaining tokens (for header)
    if new_tokens < min_remaining then
        min_remaining = new_tokens
    end

    -- Store refilled tokens (but not yet decremented)
    redis.call('HMSET', key, 'tokens', new_tokens, 'last_refill', now)
    redis.call('EXPIRE', key, 3600)
end

-- Step 2: If all levels allowed, decrement each by 1
local remaining = 0
if allowed == 1 then
    for i = 1, levels do
        local key = KEYS[i]
        -- Read the current tokens (already refilled)
        local tokens = tonumber(redis.call('HGET', key, 'tokens'))
        redis.call('HSET', key, 'tokens', tokens - 1)
    end
    remaining = math.floor(min_remaining - 1)
else
    remaining = 0
end

return {allowed, remaining}