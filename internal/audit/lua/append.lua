-- Atomically append an audit event with indexes and retention trim.
-- KEYS[1] audit:{audit}:event:{id}
-- KEYS[2] audit:{audit}:idx:ts
-- KEYS[3] audit:{audit}:idx:tenant:{tenant}
-- KEYS[4] audit:{audit}:idx:user:{user}
-- KEYS[5] audit:{audit}:idx:req:{request_id}
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
local max_events = tonumber(ARGV[11]) or 0
if max_events < 1 then
  max_events = 1
end
local ttl_sec = math.ceil(retention_ms / 1000)
if ttl_sec < 60 then ttl_sec = 60 end
-- Event hashes outlive the trim horizon so purge_event can still HGET tenant/user
-- after index TTLs have been refreshed by newer writes. Indexes keep ttl_sec.
local event_ttl = ttl_sec + 60

-- User index cap: bounds a single user's ZSET (not the global max_events).
local user_index_cap = 1000
local user_max_events = max_events
if user_max_events > user_index_cap then
  user_max_events = user_index_cap
end

local function purge_event(eid)
  local evkey = 'audit:{audit}:event:' .. eid
  local t = redis.call('HGET', evkey, 'tenant_id')
  local u = redis.call('HGET', evkey, 'user_id')
  local rid = redis.call('HGET', evkey, 'request_id')
  if t and t ~= '' then
    local tkey = 'audit:{audit}:idx:tenant:' .. t
    redis.call('ZREM', tkey, eid)
    if redis.call('ZCARD', tkey) == 0 then
      redis.call('DEL', tkey)
    end
  end
  if u and u ~= '' then
    local ukey = 'audit:{audit}:idx:user:' .. u
    redis.call('ZREM', ukey, eid)
    if redis.call('ZCARD', ukey) == 0 then
      redis.call('DEL', ukey)
    end
  end
  if rid and rid ~= '' then
    redis.call('DEL', 'audit:{audit}:idx:req:' .. rid)
  end
  redis.call('DEL', evkey)
end

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
redis.call('EXPIRE', event_key, event_ttl)

redis.call('ZADD', ts_idx, ts, id)
redis.call('ZADD', tenant_idx, ts, id)
redis.call('ZADD', user_idx, ts, id)
if request_id ~= '' and request_id ~= nil then
  redis.call('SET', req_idx, id, 'EX', event_ttl)
end

-- M-06: Set TTL on all index ZSETs so inactive users/tenants naturally expire from Redis
redis.call('EXPIRE', ts_idx, ttl_sec)
redis.call('EXPIRE', tenant_idx, ttl_sec)
redis.call('EXPIRE', user_idx, ttl_sec)

local cutoff = ts - retention_ms
local stale = redis.call('ZRANGEBYSCORE', ts_idx, 0, cutoff)
for _, eid in ipairs(stale) do
  purge_event(eid)
end
if #stale > 0 then
  redis.call('ZREMRANGEBYSCORE', ts_idx, 0, cutoff)
end

-- Global max_events trim
while redis.call('ZCARD', ts_idx) > max_events do
  local old = redis.call('ZRANGE', ts_idx, 0, 0)
  if #old == 0 then break end
  purge_event(old[1])
  redis.call('ZREM', ts_idx, old[1])
end

-- User index cap. Always ZREM user_idx: purge_event is a no-op when the event
-- hash already expired, and a missing ZREM would infinite-loop on ZCARD.
if user_id ~= nil and user_id ~= '' then
  while redis.call('ZCARD', user_idx) > user_max_events do
    local old_u = redis.call('ZRANGE', user_idx, 0, 0)
    if #old_u == 0 then break end
    purge_event(old_u[1])
    redis.call('ZREM', ts_idx, old_u[1])
    redis.call('ZREM', user_idx, old_u[1])
    if redis.call('ZCARD', user_idx) == 0 then
      redis.call('DEL', user_idx)
      break
    end
  end
end

return {1, id}
