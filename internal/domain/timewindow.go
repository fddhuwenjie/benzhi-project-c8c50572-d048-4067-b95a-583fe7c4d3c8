package domain

import "time"

// WindowContains reports whether an interval is inside the campaign window.
func (c Campaign) WindowContains(start, end time.Time) bool {
	return !start.Before(c.MissionWindowStart) && !end.After(c.MissionWindowEnd) && end.After(start)
}
