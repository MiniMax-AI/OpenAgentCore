package automations

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Schedule is a parsed five-field crontab(5) expression in a time zone.
type Schedule struct {
	minutes, hours, days, months, weekdays uint64
	// dayOr reports day fields that both do not start with "*", so a day
	// matches either. fixed reports the same of the minute and hour fields.
	dayOr, fixed bool
	location     *time.Location
}

var (
	monthNames   = []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}
	weekdayNames = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
)

// ParseSchedule parses expression, which is minute, hour, day of month, month
// and day of week. Each field is "*" or a comma list of n, a-b, */s or a-b/s;
// months and weekdays also take three-letter names, and weekday 7 is Sunday.
// It rejects descriptors, seconds and an expression with no occurrence in the
// next five years. timezone is an IANA name.
func ParseSchedule(expression, timezone string) (*Schedule, error) {
	fields := strings.Fields(expression)
	if utf8.RuneCountInString(expression) > 256 || len(fields) != 5 {
		return nil, &InvalidError{"expression", "expression must have five fields of at most 256 characters: minute, hour, day of month, month and day of week."}
	}
	if timezone == "" || timezone == "Local" || utf8.RuneCountInString(timezone) > 128 {
		return nil, &InvalidError{"timezone", "timezone must be an IANA time zone name."}
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, &InvalidError{"timezone", "timezone must be an IANA time zone name."}
	}
	s := &Schedule{location: location, dayOr: !strings.HasPrefix(fields[2], "*") && !strings.HasPrefix(fields[4], "*"), fixed: !strings.HasPrefix(fields[0], "*") && !strings.HasPrefix(fields[1], "*")}
	for i, target := range []struct {
		mask     *uint64
		min, max int
		names    []string
		first    int
	}{{&s.minutes, 0, 59, nil, 0}, {&s.hours, 0, 23, nil, 0}, {&s.days, 1, 31, nil, 0}, {&s.months, 1, 12, monthNames, 1}, {&s.weekdays, 0, 7, weekdayNames, 0}} {
		if *target.mask, err = parseField(fields[i], target.min, target.max, target.names, target.first); err != nil {
			return nil, &InvalidError{"expression", fmt.Sprintf("expression field %d %q is invalid.", i+1, fields[i])}
		}
	}
	if s.weekdays&(1<<7) != 0 {
		s.weekdays |= 1
	}
	if _, ok := s.Next(time.Now()); !ok {
		return nil, &InvalidError{"expression", "expression has no occurrence in the next five years."}
	}
	return s, nil
}

func parseField(field string, min, max int, names []string, first int) (uint64, error) {
	var mask uint64
	for _, part := range strings.Split(field, ",") {
		bounds, step, stepped := strings.Cut(part, "/")
		low, high := min, max
		if bounds != "*" {
			from, to, ranged := strings.Cut(bounds, "-")
			var err error
			if low, err = parseValue(from, names, first); err != nil {
				return 0, err
			}
			if high = low; ranged {
				if high, err = parseValue(to, names, first); err != nil {
					return 0, err
				}
			} else if stepped {
				return 0, fmt.Errorf("a step needs a range")
			}
		}
		increment := 1
		if stepped {
			var err error
			if increment, err = strconv.Atoi(step); err != nil || increment < 1 {
				return 0, fmt.Errorf("invalid step")
			}
		}
		if low < min || high > max || low > high {
			return 0, fmt.Errorf("out of range")
		}
		for v := low; v <= high; v += increment {
			mask |= 1 << v
		}
	}
	return mask, nil
}

func parseValue(text string, names []string, first int) (int, error) {
	for i, name := range names {
		if strings.EqualFold(text, name) {
			return first + i, nil
		}
	}
	if text == "" || strings.TrimLeft(text, "0123456789") != "" {
		return 0, fmt.Errorf("invalid value")
	}
	return strconv.Atoi(text)
}

// Next returns the first occurrence after the given time, or false when there
// is none in the next five years. It follows crontab(5) around daylight saving
// changes. A fixed-time Schedule, whose minute and hour fields do not start
// with "*", runs a wall-clock time in a gap at the first instant after the gap
// and a repeated one only at its first instant. Any other Schedule runs at
// every instant whose wall-clock time matches: never in a gap, on both passes
// of a repeated hour.
func (s *Schedule) Next(after time.Time) (time.Time, bool) {
	t := after.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(5, 0, 0)
	for t.Before(limit) {
		w := t.In(s.location)
		switch {
		case s.months&(1<<w.Month()) == 0:
			t = forward(t, time.Date(w.Year(), w.Month()+1, 1, 0, 0, 0, 0, s.location))
		case !s.dayMatches(w):
			t = forward(t, time.Date(w.Year(), w.Month(), w.Day()+1, 0, 0, 0, 0, s.location))
		case s.hours&(1<<w.Hour()) == 0:
			t = t.Add(time.Duration(60-w.Minute()) * time.Minute)
		case s.minutes&(1<<w.Minute()) == 0 || s.fixed && s.repeated(t):
			t = t.Add(time.Minute)
		default:
			return s.gapBefore(after, t), true
		}
	}
	return time.Time{}, false
}

// forward returns next, or the minute after t when a daylight saving gap at
// midnight turns next's missing wall-clock time into an instant before it.
func forward(t, next time.Time) time.Time {
	if next.After(t) {
		return next
	}
	return t.Add(time.Minute)
}

// repeated reports whether t is the later instant of a wall-clock minute that
// a daylight saving change repeats.
func (s *Schedule) repeated(t time.Time) bool {
	t = t.In(s.location)
	_, was := t.Add(-24 * time.Hour).Zone()
	_, is := t.Zone()
	u := t.Add(time.Duration(is-was) * time.Second)
	return u.Before(t) && u.In(s.location).Format(time.DateTime) == t.Format(time.DateTime)
}

// gapBefore returns the first instant after a daylight saving gap that holds a
// wall-clock minute a fixed-time Schedule matches, when it comes before t; else t.
func (s *Schedule) gapBefore(after, t time.Time) time.Time {
	if !s.fixed {
		return t
	}
	for z := after.In(s.location); ; {
		_, end := z.ZoneBounds()
		if end.IsZero() || !end.Before(t) {
			return t
		}
		_, was := end.Add(-time.Second).In(s.location).Zone()
		_, is := end.In(s.location).Zone()
		for c := end.UTC().Add(time.Duration(was) * time.Second); c.Before(end.UTC().Add(time.Duration(is) * time.Second)); c = c.Add(time.Minute) {
			if s.months&(1<<c.Month()) != 0 && s.dayMatches(c) && s.hours&(1<<c.Hour()) != 0 && s.minutes&(1<<c.Minute()) != 0 {
				return end
			}
		}
		z = end.In(s.location)
	}
}

func (s *Schedule) dayMatches(civil time.Time) bool {
	day, weekday := s.days&(1<<civil.Day()) != 0, s.weekdays&(1<<int(civil.Weekday())) != 0
	if s.dayOr {
		return day || weekday
	}
	return day && weekday
}
