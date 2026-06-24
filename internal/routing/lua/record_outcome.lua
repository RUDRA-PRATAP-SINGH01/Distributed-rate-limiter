-- Atomically record gateway outcome and recompute health + circuit breaker.
-- KEYS[1] = route:gw:{id}
-- ARGV[1] success (1/0)
-- ARGV[2] latency_ms
-- ARGV[3] ema_alpha
-- ARGV[4] circuit_error_rate
-- ARGV[5] circuit_min_samples
-- ARGV[6] circuit_cooldown_ms
-- ARGV[7] now_ms
-- Returns {1, health_score, latency_ema, error_rate, circuit_open} or {0}

local key = KEYS[1]
if redis.call('HEXISTS', key, 'url') == 0 then
  return {0}
end

local success = tonumber(ARGV[1])
local latency = tonumber(ARGV[2])
local alpha = tonumber(ARGV[3])
local circuit_err_rate = tonumber(ARGV[4])
local circuit_min = tonumber(ARGV[5])
local cooldown_ms = tonumber(ARGV[6])
local now = tonumber(ARGV[7])

local ema = tonumber(redis.call('HGET', key, 'latency_ema_ms') or '0')
if ema <= 0 then
  ema = latency
else
  ema = alpha * latency + (1 - alpha) * ema
end
redis.call('HSET', key, 'latency_ema_ms', string.format('%.2f', ema))

if success == 1 then
  redis.call('HINCRBY', key, 'success_count', 1)
else
  redis.call('HINCRBY', key, 'error_count', 1)
end
redis.call('HINCRBY', key, 'total_requests', 1)

local succ = tonumber(redis.call('HGET', key, 'success_count') or '0')
local err = tonumber(redis.call('HGET', key, 'error_count') or '0')
local total = succ + err

if total > 1000 then
  succ = math.floor(succ / 2)
  err = math.floor(err / 2)
  total = succ + err
  redis.call('HSET', key, 'success_count', succ, 'error_count', err)
end

local error_rate = 0
if total > 0 then
  error_rate = err / total
end

local latency_penalty = math.min(ema / 200.0, 1.0)
local health = (1 - error_rate) * (1 - latency_penalty * 0.3) * 100
if health < 0 then health = 0 end
if health > 100 then health = 100 end

local circuit_open = tonumber(redis.call('HGET', key, 'circuit_open') or '0')

if total >= circuit_min and error_rate >= circuit_err_rate then
  redis.call('HSET', key, 'circuit_open', 1, 'circuit_opened_at', now)
  circuit_open = 1
elseif success == 1 and circuit_open == 1 then
  local opened_at = tonumber(redis.call('HGET', key, 'circuit_opened_at') or '0')
  if now - opened_at >= cooldown_ms then
    redis.call('HSET', key, 'circuit_open', 0)
    circuit_open = 0
  end
end

redis.call('HSET', key,
  'health_score', string.format('%.2f', health),
  'updated_at', now
)

return {1, string.format('%.2f', health), string.format('%.2f', ema), string.format('%.4f', error_rate), circuit_open}
