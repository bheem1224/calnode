package booking

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// QueryRower is satisfied by both *sql.DB and *sql.Tx.
type QueryRower interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ISOWeek identifies an ISO 8601 week (Monday-Sunday).
type ISOWeek struct {
	Year int
	Week int
}

// FrequencyCounts holds confirmed booking counts and booked duration minutes
// bucketed by local calendar date, ISO week, month, and year.
type FrequencyCounts struct {
	ByDay         map[string]int
	ByWeek        map[ISOWeek]int
	ByMonth       map[string]int
	ByYear        map[int]int
	MinutesByDay  map[string]int
	MinutesByWeek map[ISOWeek]int
}

// Exceeded reports whether adding a booking at instant `at` in `loc` would meet or
// exceed either configured day/week frequency cap.
func (c FrequencyCounts) Exceeded(at time.Time, loc *time.Location, maxPerDay, maxPerWeek *int) bool {
	return c.ExceededAll(at, 0, loc, maxPerDay, maxPerWeek, nil, nil, nil, nil)
}

// ExceededAll reports whether adding a booking of `durMins` minutes at instant `at`
// in `loc` would violate any configured frequency or total-duration cap.
func (c FrequencyCounts) ExceededAll(at time.Time, durMins int, loc *time.Location, maxPerDay, maxPerWeek, maxPerMonth, maxPerYear, maxDurDay, maxDurWeek *int) bool {
	if loc == nil {
		loc = time.UTC
	}
	local := at.In(loc)
	dayKey := local.Format("2006-01-02")
	y, w := local.ISOWeek()
	isoWeek := ISOWeek{Year: y, Week: w}

	if maxPerDay != nil && *maxPerDay > 0 && c.ByDay[dayKey] >= *maxPerDay {
		return true
	}
	if maxPerWeek != nil && *maxPerWeek > 0 && c.ByWeek[isoWeek] >= *maxPerWeek {
		return true
	}
	if maxPerMonth != nil && *maxPerMonth > 0 && c.ByMonth[local.Format("2006-01")] >= *maxPerMonth {
		return true
	}
	if maxPerYear != nil && *maxPerYear > 0 && c.ByYear[local.Year()] >= *maxPerYear {
		return true
	}
	if maxDurDay != nil && *maxDurDay > 0 && c.MinutesByDay[dayKey]+durMins > *maxDurDay {
		return true
	}
	if maxDurWeek != nil && *maxDurWeek > 0 && c.MinutesByWeek[isoWeek]+durMins > *maxDurWeek {
		return true
	}
	return false
}

// LoadLocationOrUTC resolves an IANA timezone name, falling back to UTC on empty or invalid input.
func LoadLocationOrUTC(ianaTZ string) *time.Location {
	if ianaTZ == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(ianaTZ)
	if err != nil || loc == nil {
		return time.UTC
	}
	return loc
}

// ISOWeekWindow returns the UTC [start, end) bounds of the Monday-Sunday ISO week
// containing `at` when evaluated in `loc`.
func ISOWeekWindow(at time.Time, loc *time.Location) (time.Time, time.Time) {
	if loc == nil {
		loc = time.UTC
	}
	local := at.In(loc)
	dayMidnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	// Go Weekday(): Sunday=0, Monday=1, ..., Saturday=6. Shift so Monday=0..Sunday=6.
	wd := (int(local.Weekday()) + 6) % 7
	weekStart := dayMidnight.AddDate(0, 0, -wd)
	weekEnd := weekStart.AddDate(0, 0, 7)
	return weekStart.UTC(), weekEnd.UTC()
}

// CountFrequencyBookings counts all non-cancelled bookings for eventTypeID whose start_at
// falls near [rangeStartUTC, rangeEndUTC) (padded cleanly so timezone conversions never miss
// boundary events) and buckets them into loc's calendar days and ISO weeks.
func CountFrequencyBookings(ctx context.Context, q QueryRower, eventTypeID string, rangeStartUTC, rangeEndUTC time.Time, loc *time.Location) (FrequencyCounts, error) {
	return CountFrequencyBookingsExcluding(ctx, q, eventTypeID, rangeStartUTC, rangeEndUTC, loc, "")
}

// CountFrequencyBookingsExcluding is like CountFrequencyBookings but excludes a
// specific booking ID (used when rescheduling an existing booking).
func CountFrequencyBookingsExcluding(ctx context.Context, q QueryRower, eventTypeID string, rangeStartUTC, rangeEndUTC time.Time, loc *time.Location, excludeBookingID string) (FrequencyCounts, error) {
	if loc == nil {
		loc = time.UTC
	}
	counts := FrequencyCounts{
		ByDay:         make(map[string]int),
		ByWeek:        make(map[ISOWeek]int),
		ByMonth:       make(map[string]int),
		ByYear:        make(map[int]int),
		MinutesByDay:  make(map[string]int),
		MinutesByWeek: make(map[ISOWeek]int),
	}
	padStart := rangeStartUTC.UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	padEnd := rangeEndUTC.UTC().Add(24 * time.Hour).Format(time.RFC3339)

	rows, err := q.QueryContext(ctx, `
		SELECT start_at, end_at FROM bookings
		WHERE event_type_id = ?
		  AND status != 'cancelled'
		  AND (? = '' OR id != ?)
		  AND datetime(start_at) >= datetime(?)
		  AND datetime(start_at) < datetime(?)`,
		eventTypeID, excludeBookingID, excludeBookingID, padStart, padEnd)
	if err != nil {
		return counts, err
	}
	defer rows.Close()

	for rows.Next() {
		var rawStart, rawEnd string
		if err := rows.Scan(&rawStart, &rawEnd); err != nil {
			return counts, err
		}
		t, err := time.Parse(time.RFC3339Nano, rawStart)
		if err != nil {
			continue
		}
		durMins := 0
		if endT, err := time.Parse(time.RFC3339Nano, rawEnd); err == nil {
			if m := int(endT.Sub(t).Minutes()); m > 0 {
				durMins = m
			}
		}
		local := t.In(loc)
		dayKey := local.Format("2006-01-02")
		y, w := local.ISOWeek()
		isoWeek := ISOWeek{Year: y, Week: w}

		counts.ByDay[dayKey]++
		counts.ByWeek[isoWeek]++
		counts.ByMonth[local.Format("2006-01")]++
		counts.ByYear[local.Year()]++
		counts.MinutesByDay[dayKey] += durMins
		counts.MinutesByWeek[isoWeek] += durMins
	}
	return counts, rows.Err()
}

func enforceFrequencyCaps(ctx context.Context, tx *sql.Tx, p CreateParams, excludeBookingID string) error {
	var dbDay, dbWeek, dbMonth, dbYear, dbDurDay, dbDurWeek sql.NullInt64
	var dbTZ string
	err := tx.QueryRowContext(ctx, `
		SELECT et.max_per_day, et.max_per_week, et.max_per_month, et.max_per_year,
		       et.max_duration_per_day, et.max_duration_per_week, COALESCE(u.iana_timezone, 'UTC')
		FROM event_types et
		LEFT JOIN users u ON u.id = et.user_id
		WHERE et.id = ?`, p.EventTypeID).Scan(&dbDay, &dbWeek, &dbMonth, &dbYear, &dbDurDay, &dbDurWeek, &dbTZ)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("booking: load frequency caps: %w", err)
	}

	maxPerDay := p.MaxPerDay
	if maxPerDay == nil {
		maxPerDay = nullInt64Ptr(dbDay)
	}
	maxPerWeek := p.MaxPerWeek
	if maxPerWeek == nil {
		maxPerWeek = nullInt64Ptr(dbWeek)
	}
	maxPerMonth := nullInt64Ptr(dbMonth)
	maxPerYear := nullInt64Ptr(dbYear)
	maxDurDay := nullInt64Ptr(dbDurDay)
	maxDurWeek := nullInt64Ptr(dbDurWeek)

	if !hasPositiveCap(maxPerDay, maxPerWeek, maxPerMonth, maxPerYear, maxDurDay, maxDurWeek) {
		return nil
	}

	tzName := p.EventTimezone
	if tzName == "" {
		tzName = dbTZ
	}
	loc := LoadLocationOrUTC(tzName)
	winStart, winEnd := ISOWeekWindow(p.StartAt, loc)
	local := p.StartAt.In(loc)
	if maxPerYear != nil && *maxPerYear > 0 {
		ys := time.Date(local.Year(), 1, 1, 0, 0, 0, 0, loc).UTC()
		ye := time.Date(local.Year()+1, 1, 1, 0, 0, 0, 0, loc).UTC()
		if ys.Before(winStart) {
			winStart = ys
		}
		if ye.After(winEnd) {
			winEnd = ye
		}
	} else if maxPerMonth != nil && *maxPerMonth > 0 {
		ms := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc).UTC()
		me := time.Date(local.Year(), local.Month()+1, 1, 0, 0, 0, 0, loc).UTC()
		if ms.Before(winStart) {
			winStart = ms
		}
		if me.After(winEnd) {
			winEnd = me
		}
	}
	counts, err := CountFrequencyBookingsExcluding(ctx, tx, p.EventTypeID, winStart, winEnd, loc, excludeBookingID)
	if err != nil {
		return fmt.Errorf("booking: frequency cap check: %w", err)
	}
	durMins := 0
	if m := int(p.EndAt.Sub(p.StartAt).Minutes()); m > 0 {
		durMins = m
	}
	if counts.ExceededAll(p.StartAt, durMins, loc, maxPerDay, maxPerWeek, maxPerMonth, maxPerYear, maxDurDay, maxDurWeek) {
		return ErrFrequencyCapReached
	}
	return nil
}

func nullInt64Ptr(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int64)
	return &n
}

func hasPositiveCap(caps ...*int) bool {
	for _, c := range caps {
		if c != nil && *c > 0 {
			return true
		}
	}
	return false
}

// WithinFutureLimit checks whether startAt falls within the configured future booking
// window relative to now in ownerLoc.
//   - "calendar_days" (or ""): up to maxFutureDays*24h from now (0 = 365 days).
//   - "business_days": up to maxFutureDays Mon-Fri business days ahead of now's local date (0 = 365 calendar days).
//   - "date_range": within [rangeStart, rangeEnd] (inclusive YYYY-MM-DD in ownerLoc).
func WithinFutureLimit(now, startAt time.Time, ownerLoc *time.Location, mode string, maxFutureDays int, rangeStart, rangeEnd string) bool {
	if ownerLoc == nil {
		ownerLoc = time.UTC
	}
	nowUTC := now.UTC()
	startUTC := startAt.UTC()

	switch mode {
	case "business_days":
		if maxFutureDays <= 0 {
			return !startUTC.After(nowUTC.Add(365 * 24 * time.Hour))
		}
		cur := nowUTC.In(ownerLoc)
		day := time.Date(cur.Year(), cur.Month(), cur.Day(), 0, 0, 0, 0, ownerLoc)
		counted := 0
		for counted < maxFutureDays {
			day = day.AddDate(0, 0, 1)
			wd := day.Weekday()
			if wd != time.Saturday && wd != time.Sunday {
				counted++
			}
		}
		cutoff := day.AddDate(0, 0, 1).UTC()
		return startUTC.Before(cutoff)

	case "date_range":
		if rangeStart == "" && rangeEnd == "" {
			return !startUTC.After(nowUTC.Add(365 * 24 * time.Hour))
		}
		if rangeStart != "" {
			if rs, err := time.ParseInLocation("2006-01-02", rangeStart, ownerLoc); err == nil {
				if startUTC.Before(rs.UTC()) {
					return false
				}
			}
		}
		if rangeEnd != "" {
			if re, err := time.ParseInLocation("2006-01-02", rangeEnd, ownerLoc); err == nil {
				endExclusive := re.AddDate(0, 0, 1).UTC()
				if !startUTC.Before(endExclusive) {
					return false
				}
			}
		}
		return true

	default: // "calendar_days"
		limit := nowUTC.Add(365 * 24 * time.Hour)
		if maxFutureDays > 0 {
			limit = nowUTC.Add(time.Duration(maxFutureDays) * 24 * time.Hour)
		}
		return !startUTC.After(limit)
	}
}
