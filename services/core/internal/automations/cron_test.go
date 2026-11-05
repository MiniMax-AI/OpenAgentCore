package automations

import (
	"testing"
	"time"
)

func TestParseScheduleRejects(t *testing.T) {
	for _, test := range []struct{ expression, timezone string }{
		{"* * * *", "UTC"}, {"0 0 * * * *", "UTC"}, {"@daily", "UTC"}, {"60 * * * *", "UTC"}, {"* 24 * * *", "UTC"},
		{"*/0 * * * *", "UTC"}, {"5/2 * * * *", "UTC"}, {"* * 0 * *", "UTC"}, {"* * * 13 *", "UTC"}, {"* * * * 8", "UTC"},
		{"0 0 30 2 *", "UTC"}, {"* * * * *", ""}, {"* * * * *", "Local"}, {"* * * * *", "Mars/Base"},
	} {
		if _, err := ParseSchedule(test.expression, test.timezone); err == nil {
			t.Errorf("ParseSchedule(%q, %q) accepted", test.expression, test.timezone)
		}
	}
}

func TestScheduleNext(t *testing.T) {
	for _, test := range []struct {
		name, expression, timezone, after, want string
	}{
		{"every minute", "* * * * *", "UTC", "2026-03-01T10:00:30Z", "2026-03-01T10:01:00Z"},
		{"steps and lists", "0,30 */6 * * *", "UTC", "2026-03-01T06:30:00Z", "2026-03-01T12:00:00Z"},
		{"names and ranges", "0 9 * jan-mar mon-fri", "UTC", "2026-03-06T09:00:00Z", "2026-03-09T09:00:00Z"},
		{"Sunday as 7", "0 0 * * 7", "UTC", "2026-03-01T00:00:00Z", "2026-03-08T00:00:00Z"},
		{"February 29", "0 0 29 2 *", "UTC", "2026-03-01T00:00:00Z", "2028-02-29T00:00:00Z"},
		{"day or weekday", "0 0 13 * fri", "UTC", "2026-03-01T00:00:00Z", "2026-03-06T00:00:00Z"},
		{"zone offset", "0 9 * * *", "Asia/Shanghai", "2026-03-01T00:00:00Z", "2026-03-01T01:00:00Z"},
		{"nonexistent minute is skipped", "30 2 * * *", "America/New_York", "2026-03-07T12:00:00Z", "2026-03-09T06:30:00Z"},
		{"repeated hour fires once", "30 1 * * *", "America/New_York", "2026-10-31T12:00:00Z", "2026-11-01T05:30:00Z"},
		{"repeated hour east of UTC fires at its first instant", "30 2 * * *", "Europe/Berlin", "2026-10-24T12:00:00Z", "2026-10-25T00:30:00Z"},
		{"after the repeated hour", "30 1 * * *", "America/New_York", "2026-11-01T05:30:00Z", "2026-11-02T06:30:00Z"},
	} {
		schedule, err := ParseSchedule(test.expression, test.timezone)
		if err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		after, _ := time.Parse(time.RFC3339, test.after)
		got, ok := schedule.Next(after)
		if !ok || got.UTC().Format(time.RFC3339) != test.want {
			t.Errorf("%s: Next(%s) = %s %v, want %s", test.name, test.after, got.UTC().Format(time.RFC3339), ok, test.want)
		}
	}
}
