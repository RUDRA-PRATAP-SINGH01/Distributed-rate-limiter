-- Atomically mark an idempotency record as failed (retryable).
-- KEYS[1] = idem:{scope}:meta:{key}
-- ARGV[1] = http_status
-- ARGV[2] = resp_headers (JSON)
-- ARGV[3] = error body
-- ARGV[4] = failed_ttl_ms (LockTTL; transient, not CompletedTTL)
-- ARGV[5] = now_ms
-- ARGV[6] = fence_token (must match current owner)
--
-- Returns {1} on success, {0} if not owner or not in processing state.

local meta_key = KEYS[1]
local http_status = ARGV[1]
local headers_json = ARGV[2]
local body = ARGV[3]
local failed_ttl_ms = tonumber(ARGV[4])
local now_ms = tonumber(ARGV[5])
local fence_token = ARGV[6]

local status = redis.call('HGET', meta_key, 'status')
if status ~= 'processing' then
  return {0}
end

local current_fence = redis.call('HGET', meta_key, 'fence_token')
if current_fence ~= fence_token then
  return {0}
end

redis.call('HSET', meta_key,
  'status', 'failed',
  'http_status', http_status,
  'resp_headers', headers_json,
  'resp_body', body,
  'body_ref', 'inline',
  'lock_until', now_ms + failed_ttl_ms,
  'failed_at', now_ms
)

redis.call('PEXPIRE', meta_key, failed_ttl_ms)
return {1}
