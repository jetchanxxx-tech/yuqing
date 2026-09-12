package id

import "time"

// Time decodes the generation timestamp embedded in a ULID produced by New.
//
// 用途：ULID 本身携带毫秒时间戳，所以「只存了 id 的表」（例如平台的
// api_keys 表没有 created_at 列）仍能还原行的创建时刻，无需改表结构。
//
// 只接受 New 产出的 Crockford 大写字母表（小写亦接受，base32 大小写不敏感）；
// 歧义字符 I/L/O/U 一律判为非法 —— 静默猜测会把损坏的 ID 解成一个看似
// 合理的时间，从而伪造出错误的审计信息。
//
// 返回 ok=false 表示 s 不是合法 ULID。非 ULID ID 的调用方应自行降级
// （例如返回零值时间），不要使用返回的时间。
func Time(s string) (time.Time, bool) {
	if len(s) < timestampLen {
		return time.Time{}, false
	}

	var ms uint64
	for i := 0; i < timestampLen; i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		v := indexOf(c)
		if v < 0 {
			return time.Time{}, false
		}
		ms = ms*32 + uint64(v)
	}
	// 10 个字符可编码 50 位，ULID 时间戳只用低 48 位。
	return time.UnixMilli(int64(ms)).UTC(), true
}

// indexOf returns the alphabet position of c, or -1 when c is not a letter
// of the Crockford base32 alphabet used by ULID.
func indexOf(c byte) int {
	for i := 0; i < len(encoding); i++ {
		if encoding[i] == c {
			return i
		}
	}
	return -1
}
