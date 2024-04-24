local val = redis.call('get', KEYS[1])
local expiration = ARGV[1]
local limit = tonumber(ARGV[2])
if val == false then
    if limit < 1 then
        return "true"
    else
        redis.call('set', KEYS[1], 1, 'PX', expiration)
        return "false"
    end
elseif tonumber(val) < limit then
    redis.call('incr', KEYS[1])
    return "false"
else
    return "true"
end