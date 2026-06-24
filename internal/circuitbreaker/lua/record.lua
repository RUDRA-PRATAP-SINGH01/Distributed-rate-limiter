-- Record outcome and transition circuit state atomically.
-- KEYS[1] = cb:{target}
-- ARGV[1] outcome (0=success, 1=failure, 2=timeout, 3=latency_spike)
-- ARGV[2] latency_ms
-- ARGV[3] now_ms
-- ARGV[4] failure_rate_threshold
-- ARGV[5] min_samples
-- ARGV[6] consecutive_failures_threshold
-- ARGV[7] latency_threshold_ms
-- ARGV[8] timeout_rate_threshold
-- ARGV[9] half_open_success_required
-- ARGV[10] ema_alpha
-- Returns {ok, state_code, prev_state_code, transition, failure_rate, latency_ema}

local key = KEYS[1]
local outcome = tonumber(ARGV[1])
local latency = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local fail_rate_thresh = tonumber(ARGV[4])
local min_samples = tonumber(ARGV[5])
local consec_thresh = tonumber(ARGV[6])
local latency_thresh = tonumber(ARGV[7])
local timeout_rate_thresh = tonumber(ARGV[8])
local half_open_success_req = tonumber(ARGV[9])
local alpha = tonumber(ARGV[10])

if redis.call('EXISTS', key) == 0 then
  redis.call('HSET', key, 'state', 'closed', 'consecutive_failures', 0,
    'success_count', 0, 'failure_count', 0, 'timeout_count', 0,
    'latency_spike_count', 0, 'total_count', 0, 'latency_ema_ms', 0)
end

local prev_state = redis.call('HGET', key, 'state') or 'closed'
local state = prev_state
local transition = 'none'

local ema = tonumber(redis.call('HGET', key, 'latency_ema_ms') or '0')
if ema <= 0 then
  ema = latency
else
  ema = alpha * latency + (1 - alpha) * ema
end
redis.call('HSET', key, 'latency_ema_ms', string.format('%.2f', ema))

redis.call('HINCRBY', key, 'total_count', 1)

if outcome == 0 then
  redis.call('HINCRBY', key, 'success_count', 1)
  redis.call('HSET', key, 'consecutive_failures', 0)
elseif outcome == 2 then
  redis.call('HINCRBY', key, 'timeout_count', 1)
  redis.call('HINCRBY', key, 'failure_count', 1)
  redis.call('HINCRBY', key, 'consecutive_failures', 1)
elseif outcome == 3 then
  redis.call('HINCRBY', key, 'latency_spike_count', 1)
  redis.call('HINCRBY', key, 'failure_count', 1)
  redis.call('HINCRBY', key, 'consecutive_failures', 1)
else
  redis.call('HINCRBY', key, 'failure_count', 1)
  redis.call('HINCRBY', key, 'consecutive_failures', 1)
end

local total = tonumber(redis.call('HGET', key, 'total_count') or '0')
if total > 1000 then
  for _, field in ipairs({'success_count', 'failure_count', 'timeout_count', 'latency_spike_count', 'total_count'}) do
    local v = math.floor(tonumber(redis.call('HGET', key, field) or '0') / 2)
    redis.call('HSET', key, field, v)
  end
  total = math.floor(total / 2)
end

local fail = tonumber(redis.call('HGET', key, 'failure_count') or '0')
local timeouts = tonumber(redis.call('HGET', key, 'timeout_count') or '0')

local failure_rate = 0
if total > 0 then failure_rate = fail / total end
local timeout_rate = 0
if total > 0 then timeout_rate = timeouts / total end

local consec = tonumber(redis.call('HGET', key, 'consecutive_failures') or '0')

local function open_circuit()
  redis.call('HSET', key, 'state', 'open', 'opened_at', now)
  state = 'open'
  transition = 'opened'
end

local function close_circuit()
  redis.call('HSET', key, 'state', 'closed', 'consecutive_failures', 0,
    'half_open_calls', 0, 'half_open_successes', 0)
  state = 'closed'
  transition = 'closed'
end

if state == 'half_open' then
  if outcome == 0 then
    local hs = redis.call('HINCRBY', key, 'half_open_successes', 1)
    if hs >= half_open_success_req then
      close_circuit()
    end
  else
    open_circuit()
    transition = 'reopened'
  end
elseif state == 'closed' then
  local trip = false
  if total >= min_samples and failure_rate >= fail_rate_thresh then trip = true end
  if consec >= consec_thresh then trip = true end
  if latency >= latency_thresh and ema >= latency_thresh then trip = true end
  if total >= min_samples and timeout_rate >= timeout_rate_thresh then trip = true end
  if trip then open_circuit() end
end

redis.call('HSET', key, 'updated_at', now)

local state_code = 0
if state == 'open' then state_code = 1
elseif state == 'half_open' then state_code = 2 end

local prev_code = 0
if prev_state == 'open' then prev_code = 1
elseif prev_state == 'half_open' then prev_code = 2 end

return {1, state_code, prev_code, transition,
  string.format('%.4f', failure_rate), string.format('%.2f', ema)}
