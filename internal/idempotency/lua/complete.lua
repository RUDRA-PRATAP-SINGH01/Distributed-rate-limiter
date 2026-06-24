-- Atomically store a completed idempotency response.
-- KEYS[1] = idem:{scope}:{key}
-- KEYS[2] = idem:body:{scope}:{key}
-- ARGV[1] = http_status
-- ARGV[2] = resp_headers (JSON)
-- ARGV[3] = response body
-- ARGV[4] = completed_ttl_ms
-- ARGV[5] = now_ms
-- ARGV[6] = inline_threshold bytes
--
-- Returns {1} on success, {0} if not in processing state.

local meta_key = KEYS[1]
local body_key = KEYS[2]
local http_status = ARGV[1]
local headers_json = ARGV[2]
local body = ARGV[3]
local completed_ttl_ms = tonumber(ARGV[4])
local now_ms = tonumber(ARGV[5])
local inline_threshold = tonumber(ARGV[6]) or 65536

local status = redis.call('HGET', meta_key, 'status')
if status ~= 'processing' then
  return {0}
end

redis.call('HSET', meta_key,
  'status', 'completed',
  'http_status', http_status,
  'resp_headers', headers_json,
  'completed_at', now_ms
)

if #body > inline_threshold then
  redis.call('SET', body_key, body)
  redis.call('PEXPIRE', body_key, completed_ttl_ms)
  redis.call('HSET', meta_key, 'body_ref', 'external')
  redis.call('HDEL', meta_key, 'resp_body')
else
  redis.call('HSET', meta_key, 'resp_body', body, 'body_ref', 'inline')
  redis.call('DEL', body_key)
end

redis.call('PEXPIRE', meta_key, completed_ttl_ms)
return {1}
