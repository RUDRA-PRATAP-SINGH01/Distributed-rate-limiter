-- Atomic idempotency claim: new, replay, in-progress, hash mismatch, or reclaim expired lock.
-- KEYS[1] = idem:{scope}:meta:{key}  (HASH metadata; {scope} hash tag for Cluster)
-- KEYS[2] = idem:{scope}:body:{key}  (STRING large response body; same slot as KEYS[1])
-- ARGV[1] = request_hash
-- ARGV[2] = now_ms
-- ARGV[3] = lock_ttl_ms
-- ARGV[4] = completed_ttl_ms (unused on claim, reserved)
-- ARGV[5] = fence_token (new owner token; stale holders cannot complete)
--
-- Returns:
--   {1, fence_token}            -> claimed (new or reclaimed)
--   {2, http_status, headers, body} -> replay cached response
--   {3, retry_after_ms}         -> in progress
--   {0}                         -> hash mismatch

local meta_key = KEYS[1]
local body_key = KEYS[2]
local req_hash = ARGV[1]
local now_ms = tonumber(ARGV[2])
local lock_ttl_ms = tonumber(ARGV[3])
local fence_token = ARGV[5]

local status = redis.call('HGET', meta_key, 'status')

if not status then
  redis.call('HSET', meta_key,
    'status', 'processing',
    'request_hash', req_hash,
    'created_at', now_ms,
    'lock_until', now_ms + lock_ttl_ms,
    'fence_token', fence_token
  )
  redis.call('PEXPIRE', meta_key, lock_ttl_ms)
  return {1, fence_token}
end

local existing_hash = redis.call('HGET', meta_key, 'request_hash')
if existing_hash ~= req_hash then
  return {0}
end

if status == 'completed' then
  local http_status = redis.call('HGET', meta_key, 'http_status') or '200'
  local headers = redis.call('HGET', meta_key, 'resp_headers') or ''
  local body_ref = redis.call('HGET', meta_key, 'body_ref') or 'inline'
  local body = ''
  if body_ref == 'external' then
    body = redis.call('GET', body_key) or ''
  else
    body = redis.call('HGET', meta_key, 'resp_body') or ''
  end
  return {2, http_status, headers, body}
end

if status == 'processing' or status == 'failed' then
  local lock_until = tonumber(redis.call('HGET', meta_key, 'lock_until') or '0')
  if now_ms < lock_until then
    if status == 'failed' then
      local http_status = redis.call('HGET', meta_key, 'http_status') or '500'
      local headers = redis.call('HGET', meta_key, 'resp_headers') or ''
      local body = redis.call('HGET', meta_key, 'resp_body') or ''
      return {2, http_status, headers, body}
    end
    return {3, lock_until - now_ms}
  end
  redis.call('HSET', meta_key,
    'status', 'processing',
    'lock_until', now_ms + lock_ttl_ms,
    'fence_token', fence_token
  )
  redis.call('PEXPIRE', meta_key, lock_ttl_ms)
  return {1, fence_token}
end

return {0}
