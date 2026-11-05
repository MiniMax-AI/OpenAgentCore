package automations

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a parsed five-field crontab(5) expression in a time zone.
type Schedule struct {
	minutes, hours, days, months, weekdays uint64
	// anyDay and anyWeekday report a day field that starts with "*". When
	// neither does, a day matches either field.
	anyDay, anyWeekday bool
	location           *time.Location
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
	if len(fields) != 5 {
		return nil, &InvalidError{"expression", "expression must have five fields: minute, hour, day of month, month and day of week."}
	}
	if timezone == "" || timezone == "Local" {
		return nil, &InvalidError{"timezone", "timezone must be an IANA time zone name."}
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, &InvalidError{"timezone", "timezone must be an IANA time zone name."}
	}
	s := &Schedule{location: location, anyDay: strings.HasPrefix(fields[2], "*"), anyWeekday: strings.HasPrefix(fields[4], "*")}
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
// is none in the next five years. It walks wall-clock minutes in the Schedule's
// zone. A wall-clock minute that does not exist is skipped, and one that occurs
// twice fires once, at its first instant.
func (s *Schedule) Next(after time.Time) (time.Time, bool) {
	a := after.In(s.location)
	civil := time.Date(a.Year(), a.Month(), a.Day(), a.Hour(), a.Minute()+1, 0, 0, time.UTC)
	limit := civil.AddDate(5, 0, 0)
	for civil.Before(limit) {
		year, month, day := civil.Date()
		switch {
		case s.months&(1<<month) == 0:
			civil = time.Date(year, month+1, 1, 0, 0, 0, 0, time.UTC)
		case !s.dayMatches(civil):
			civil = time.Date(year, month, day+1, 0, 0, 0, 0, time.UTC)
		case s.hours&(1<<civil.Hour()) == 0:
			civil = time.Date(year, month, day, civil.Hour()+1, 0, 0, 0, time.UTC)
		case s.minutes&(1<<civil.Minute()) == 0:
			civil = civil.Add(time.Minute)
		default:
			t := time.Date(year, month, day, civil.Hour(), civil.Minute(), 0, 0, s.location)
			if y, m, d := t.Date(); t.After(after) && y == year && m == month && d == day && t.Hour() == civil.Hour() && t.Minute() == civil.Minute() {
				return t, true
			}
			civil = civil.Add(time.Minute)
		}
	}
	return time.Time{}, false
}

func (s *Schedule) dayMatches(civil time.Time) bool {
	day, weekday := s.days&(1<<civil.Day()) != 0, s.weekdays&(1<<int(civil.Weekday())) != 0
	if s.anyDay || s.anyWeekday {
		return day && weekday
	}
	return day || weekday
}
