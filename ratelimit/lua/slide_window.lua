-- Rate limit check using Redis sorted set
-- KEYS[1] : The key for the rate limiter
-- ARGV[1] : The window of time for rate limiting (milliseconds)
-- ARGV[2] : The maximum number of allowed requests in the window
-- ARGV[3] : The current timestamp (milliseconds)

-- Define variables
local key = KEYS[1]
local window = tonumber(ARGV[1])
local threshold = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local min = now - window

-- Remove old entries outside of the current window
redis.call('ZREMRANGEBYSCORE', key, '-inf', min)

-- Count the number of requests in the window
local cnt = redis.call('ZCOUNT', key, '-inf', '+inf')

-- Check if the number of requests exceeds the threshold
if cnt >= threshold then
    -- Return "true" when the rate limit has been reached
    return "true"
else
    -- Add the current timestamp to the sorted set and reset the expiration of the key
    redis.call('ZADD', key, now, now)
    redis.call('PEXPIRE', key, window)
    -- Return "false" since the rate limit has not been reached
    return "false"
end