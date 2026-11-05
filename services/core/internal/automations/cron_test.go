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
		{"fixed time in a gap runs after the gap", "30 2 * * *", "America/New_York", "2026-03-07T12:00:00Z", "2026-03-08T07:00:00Z"},
		{"wildcard skips a gap", "30 * * * *", "America/New_York", "2026-03-08T06:45:00Z", "2026-03-08T07:30:00Z"},
		{"wildcard runs both passes", "0,30 * * * *", "Europe/Berlin", "2026-10-25T00:30:00Z", "2026-10-25T01:00:00Z"},
		{"wildcard second pass, half hour", "0,30 * * * *", "Europe/Berlin", "2026-10-25T01:00:00Z", "2026-10-25T01:30:00Z"},
		{"fixed time east of UTC runs once", "30 2 * * *", "Europe/Berlin", "2026-10-25T00:30:00Z", "2026-10-26T01:30:00Z"},
		{"repeated hour fires once", "30 1 * * *", "America/New_York", "2026-10-31T12:00:00Z", "2026-11-01T05:30:00Z"},
		{"repeated hour east of UTC fires at its first instant", "30 2 * * *", "Europe/Berlin", "2026-10-24T12:00:00Z", "2026-10-25T00:30:00Z"},
		{"after the repeated hour", "30 1 * * *", "America/New_York", "2026-11-01T05:30:00Z", "2026-11-02T06:30:00Z"},
		{"day search across a gap at midnight", "0 9 * 9 1", "America/Santiago", "2026-08-01T00:00:00Z", "2026-09-07T12:00:00Z"},
		{"fixed midnight in a gap runs after the gap", "0 0 * * *", "America/Santiago", "2026-09-05T12:00:00Z", "2026-09-06T04:00:00Z"},
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

// A step schedule keeps every occurrence of the repeated hour: 12 per pass.
func TestWildcardKeepsRepeatedHour(t *testing.T) {
	schedule, err := ParseSchedule("*/5 * * * *", "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	at, _ := time.Parse(time.RFC3339, "2026-11-01T04:55:00Z")
	count := 0
	for ; at.Before(time.Date(2026, 11, 1, 7, 0, 0, 0, time.UTC)); at, _ = schedule.Next(at) {
		if w := at.In(schedule.location); w.Hour() == 1 {
			count++
		}
	}
	if count != 24 {
		t.Errorf("occurrences in the repeated hour: %d, want 24", count)
	}
}
