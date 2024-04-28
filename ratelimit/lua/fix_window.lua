-- Rate limit check using Redis
-- ARGV[1]: Expiration time in milliseconds
-- ARGV[2]: Request limit count

-- Get the current count for the key
local val = redis.call('get', KEYS[1])
local expiration = ARGV[1]
local limit = tonumber(ARGV[2])

-- If the key does not exist in Redis
if val == false then
    -- Check if limit is set to less than 1 which implies no requests should be allowed
    if limit < 1 then
        return "true"  -- Indicate that the limit is reached
    else
        -- Since the request count is below the limit, set the key with initial count 1 and set its expiration
        redis.call('set', KEYS[1], 1, 'PX', expiration)
        return "false"  -- Indicate that the limit is not reached
    end
    -- If the key exists and the count is below the limit
elseif tonumber(val) < limit then
    -- Increment the count for the key
    redis.call('incr', KEYS[1])
    return "false"  -- Indicate that the limit is not reached
else
    -- When count exceeds the limit
    return "true"  -- Indicate that the limit is reached
end