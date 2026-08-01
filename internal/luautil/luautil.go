// Package luautil provides common helpers for handling Lua scripts and Redis interface conversions.
package luautil

import (
	"fmt"
	"strconv"
)

// LuaInt normalizes Redis/Lua return types — RESP encodings differ by version and driver.
// An unparsable string yields 0, matching the zero value callers already treat
// as "no value" for counters and remaining quota.
func LuaInt(v interface{}) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	case string:
		parsed, err := strconv.ParseInt(n, 10, 64)
		if err != nil {
			return 0
		}
		return parsed
	default:
		return 0
	}
}

// LuaString normalizes Redis/Lua return types to string.
func LuaString(v interface{}) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	default:
		return fmt.Sprint(v)
	}
}
