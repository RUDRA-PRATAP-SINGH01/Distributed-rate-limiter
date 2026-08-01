-- KEYS[1..4]: global_key, tenant_key, user_key, endpoint_key
-- ARGV[1..4]: capacities (global, tenant, user, endpoint)
-- ARGV[5..8]: refill_rates (global, tenant, user, endpoint)
-- ARGV[9]: current timestamp (milliseconds)
-- ARGV[10]: requested tokens (always 1)
--
-- Per-level TTL = ceil(capacity / refill_rate), clamped to [1, 86400] (L-02).

local levels = 4
local allowed = 1
local min_remaining = math.huge
local now = tonumber(ARGV[9])
local requested = tonumber(ARGV[10])

local function bucket_ttl_sec(cap, rate)
    if cap == nil or rate == nil or rate <= 0 then
        return 1
    end
    local t = math.ceil(cap / rate)
    if t < 1 then
        t = 1
    end
    if t > 86400 then
        t = 86400
    end
    return t
end

local level_new_tokens = {}
local level_ttl = {}

-- Step 1: Refill and check each level
for i = 1, levels do
    local key = KEYS[i]
    local capacity = tonumber(ARGV[i])
    local refill_rate = tonumber(ARGV[4 + i])
    level_ttl[i] = bucket_ttl_sec(capacity, refill_rate)

    -- Read bucket state
    local bucket = redis.call('HMGET', key, 'tokens', 'last_refill')
    local tokens = tonumber(bucket[1])
    local last_refill = tonumber(bucket[2])

    if tokens == nil then
        tokens = capacity
        last_refill = now
    end

    -- Refill based on elapsed time (in ms)
    local elapsed = (now - last_refill) / 1000.0
    if elapsed < 0 then
        elapsed = 0
    end
    local new_tokens = tokens + elapsed * refill_rate
    if new_tokens > capacity then
        new_tokens = capacity
    end

    level_new_tokens[i] = new_tokens

    -- Check if this level can allow the request
    if math.floor(new_tokens) < requested then
        allowed = 0
    end

    -- Track the smallest remaining tokens (floor)
    local rem = math.floor(new_tokens)
    if rem < min_remaining then
        min_remaining = rem
    end
end

-- Step 2: Write back and return
local remaining = 0
if allowed == 1 then
    for i = 1, levels do
        local key = KEYS[i]
        local updated_tokens = level_new_tokens[i] - requested
        redis.call('HMSET', key, 'tokens', updated_tokens, 'last_refill', now)
        redis.call('EXPIRE', key, level_ttl[i])
    end
    remaining = math.floor(min_remaining - requested)
else
    for i = 1, levels do
        local key = KEYS[i]
        redis.call('HMSET', key, 'tokens', level_new_tokens[i], 'last_refill', now)
        redis.call('EXPIRE', key, level_ttl[i])
    end
    remaining = 0
end

return {allowed, remaining}
