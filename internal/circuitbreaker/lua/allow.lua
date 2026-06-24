-- Atomic allow check with open -> half-open transition on cooldown expiry.
-- KEYS[1] = cb:{target}
-- ARGV[1] = now_ms
-- ARGV[2] = open_cooldown_ms
-- ARGV[3] = half_open_max_probes
-- Returns {allowed, state_code, probes_remaining}
-- state_code: 0=closed, 1=open, 2=half_open

local key = KEYS[1]
local now = tonumber(ARGV[1])
local cooldown = tonumber(ARGV[2])
local max_probes = tonumber(ARGV[3])

if redis.call('EXISTS', key) == 0 then
  redis.call('HSET', key, 'state', 'closed', 'consecutive_failures', 0,
    'success_count', 0, 'failure_count', 0, 'timeout_count', 0,
    'latency_spike_count', 0, 'total_count', 0, 'latency_ema_ms', 0)
end

local state = redis.call('HGET', key, 'state') or 'closed'

if state == 'open' then
  local opened_at = tonumber(redis.call('HGET', key, 'opened_at') or '0')
  if now - opened_at >= cooldown then
    redis.call('HSET', key, 'state', 'half_open', 'half_open_at', now,
      'half_open_calls', 0, 'half_open_successes', 0)
    state = 'half_open'
  else
    return {0, 1, 0}
  end
end

if state == 'half_open' then
  local probes = tonumber(redis.call('HGET', key, 'half_open_calls') or '0')
  if probes >= max_probes then
    -- Probe budget exhausted without recovery — reopen so cooldown can retry.
    redis.call('HSET', key, 'state', 'open', 'opened_at', now,
      'half_open_calls', 0, 'half_open_successes', 0)
    return {0, 1, 0}
  end
  redis.call('HINCRBY', key, 'half_open_calls', 1)
  return {1, 2, max_probes - probes - 1}
end

return {1, 0, -1}
