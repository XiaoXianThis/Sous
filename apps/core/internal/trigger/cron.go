// Package trigger starts supervisor turns that no user typed (design §9):
// schedules (cron), and follow-ups on tasks that are stuck, runs that take
// too long, approvals and questions left waiting. It also runs the daily
// data retention.
package trigger

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Cron is a parsed five-field cron expression: minute hour day-of-month
// month day-of-week. Fields accept *, numbers, ranges (a-b), lists (a,b)
// and steps (*/n, a-b/n). Day of week is 0-7 (0 and 7 are Sunday). As in
// standard cron, when both day fields are restricted a day matching either
// one counts. @hourly, @daily, @weekly, @monthly and @yearly are accepted.
type Cron struct {
	min, hour, dom, month, dow uint64
	domAny, dowAny             bool
}

var aliases = map[string]string{
	"@hourly": "0 * * * *", "@daily": "0 0 * * *", "@midnight": "0 0 * * *",
	"@weekly": "0 0 * * 0", "@monthly": "0 0 1 * *", "@yearly": "0 0 1 1 *", "@annually": "0 0 1 1 *",
}

// ParseCron parses an expression.
func ParseCron(expr string) (Cron, error) {
	expr = strings.TrimSpace(expr)
	if a, ok := aliases[strings.ToLower(expr)]; ok {
		expr = a
	}
	f := strings.Fields(expr)
	if len(f) != 5 {
		return Cron{}, fmt.Errorf("cron 表达式要有 5 个字段（分 时 日 月 周），收到 %q", expr)
	}
	var c Cron
	var err error
	if c.min, err = field(f[0], 0, 59); err != nil {
		return Cron{}, fmt.Errorf("分钟：%w", err)
	}
	if c.hour, err = field(f[1], 0, 23); err != nil {
		return Cron{}, fmt.Errorf("小时：%w", err)
	}
	if c.dom, err = field(f[2], 1, 31); err != nil {
		return Cron{}, fmt.Errorf("日：%w", err)
	}
	if c.month, err = field(f[3], 1, 12); err != nil {
		return Cron{}, fmt.Errorf("月：%w", err)
	}
	if c.dow, err = field(f[4], 0, 7); err != nil {
		return Cron{}, fmt.Errorf("星期：%w", err)
	}
	if c.dow&(1<<7) != 0 {
		c.dow |= 1 // 7 is Sunday too
	}
	c.domAny, c.dowAny = f[2] == "*", f[4] == "*"
	return c, nil
}

func field(s string, lo, hi int) (uint64, error) {
	var bits uint64
	for _, part := range strings.Split(s, ",") {
		rng, stepS, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepS)
			if err != nil || n <= 0 {
				return 0, fmt.Errorf("步长 %q 无效", stepS)
			}
			step = n
		}
		a, b := lo, hi
		switch {
		case rng == "*":
		case strings.Contains(rng, "-"):
			x, y, _ := strings.Cut(rng, "-")
			var err1, err2 error
			a, err1 = strconv.Atoi(x)
			b, err2 = strconv.Atoi(y)
			if err1 != nil || err2 != nil || a > b {
				return 0, fmt.Errorf("范围 %q 无效", rng)
			}
		default:
			n, err := strconv.Atoi(rng)
			if err != nil {
				return 0, fmt.Errorf("%q 不是数字", rng)
			}
			a = n
			if !hasStep {
				b = n
			}
		}
		if a < lo || b > hi {
			return 0, fmt.Errorf("%q 超出范围 %d-%d", part, lo, hi)
		}
		for i := a; i <= b; i += step {
			bits |= 1 << uint(i)
		}
	}
	return bits, nil
}

func has(bits uint64, n int) bool { return bits&(1<<uint(n)) != 0 }

func (c Cron) dayMatches(t time.Time) bool {
	dom, dow := has(c.dom, t.Day()), has(c.dow, int(t.Weekday()))
	switch {
	case c.domAny && c.dowAny:
		return true
	case c.domAny:
		return dow
	case c.dowAny:
		return dom
	}
	return dom || dow
}

// Next returns the first time after t (in loc) that the expression
// matches, or the zero time if there is none within five years.
func (c Cron) Next(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc).Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(5, 0, 0)
	for t.Before(limit) {
		switch {
		case !has(c.month, int(t.Month())):
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, loc)
		case !c.dayMatches(t):
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
		case !has(c.hour, t.Hour()):
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, loc)
		case !has(c.min, t.Minute()):
			t = t.Add(time.Minute)
		default:
			return t
		}
	}
	return time.Time{}
}
