-- Atomically append an audit event with indexes and retention trim.
-- KEYS[1] audit:event:{id}
-- KEYS[2] audit:idx:ts
-- KEYS[3] audit:idx:tenant:{tenant}
-- KEYS[4] audit:idx:user:{user}
-- KEYS[5] audit:idx:req:{request_id}
-- ARGV[1..8] id, request_id, tenant_id, user_id, decision, reason, handler, remaining
-- ARGV[9] timestamp_ms
-- ARGV[10] retention_ms
-- ARGV[11] max_events

local event_key = KEYS[1]
local ts_idx = KEYS[2]
local tenant_idx = KEYS[3]
local user_idx = KEYS[4]
local req_idx = KEYS[5]

local id = ARGV[1]
local request_id = ARGV[2]
local tenant_id = ARGV[3]
local user_id = ARGV[4]
local decision = ARGV[5]
local reason = ARGV[6]
local handler = ARGV[7]
local remaining = ARGV[8]
local ts = tonumber(ARGV[9])
local retention_ms = tonumber(ARGV[10])
local max_events = tonumber(ARGV[11])
local ttl_sec = math.ceil(retention_ms / 1000)
if ttl_sec < 60 then ttl_sec = 60 end

redis.call('HSET', event_key,
  'id', id,
  'request_id', request_id,
  'tenant_id', tenant_id,
  'user_id', user_id,
  'decision', decision,
  'reason', reason,
  'handler', handler,
  'remaining', remaining,
  'timestamp_ms', ts
)
redis.call('EXPIRE', event_key, ttl_sec)

redis.call('ZADD', ts_idx, ts, id)
redis.call('ZADD', tenant_idx, ts, id)
redis.call('ZADD', user_idx, ts, id)
if request_id ~= '' and request_id ~= nil then
  redis.call('SET', req_idx, id, 'EX', ttl_sec)
end

local cutoff = ts - retention_ms
local stale = redis.call('ZRANGEBYSCORE', ts_idx, 0, cutoff)
for _, eid in ipairs(stale) do
  redis.call('DEL', 'audit:event:' .. eid)
end
if #stale > 0 then
  redis.call('ZREMRANGEBYSCORE', ts_idx, 0, cutoff)
end

while redis.call('ZCARD', ts_idx) > max_events do
  local old = redis.call('ZRANGE', ts_idx, 0, 0)
  if #old == 0 then break end
  redis.call('DEL', 'audit:event:' .. old[1])
  redis.call('ZREM', ts_idx, old[1])
end

return {1, id}
