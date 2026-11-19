package connstore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

const CalendarReferencePrefix = "calnode:calendar:"

type calendarReference struct {
	Provider string `json:"p"`
	Calendar string `json:"c"`
	Account  string `json:"a"`
}

// References contain account identity, not tokens. Only persist them internally;
// delegation UIs receive opaque selection IDs instead of account addresses.
func CalendarReference(provider, calendarID, account string) string {
	raw, _ := json.Marshal(calendarReference{provider, calendarID, account})
	return CalendarReferencePrefix + base64.RawURLEncoding.EncodeToString(raw)
}

// CalendarTarget binds an opaque saved selection to its account. Keep this
// reference on a booking so aliases such as "primary" cannot switch accounts
// after the user's destination changes. Legacy provider IDs remain supported.
func CalendarTarget(ctx context.Context, q Execer, userID, reference string) (provider, calendarID, accountEmail string, err error) {
	if IsCalendarReference(reference) {
		var target calendarReference
		if len(reference) > 8192 {
			err = fmt.Errorf("invalid calendar reference")
			return
		}
		raw, e := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(reference, CalendarReferencePrefix))
		if e != nil || json.Unmarshal(raw, &target) != nil || target.Calendar == "" || (target.Provider != "google" && target.Provider != "microsoft") {
			err = fmt.Errorf("invalid calendar reference")
			return
		}
		return target.Provider, target.Calendar, target.Account, nil
	}
	err = q.QueryRowContext(ctx, `SELECT provider,calendar_id,account_email FROM connection_calendars WHERE user_id=? AND calendar_id=? ORDER BY is_destination DESC,id LIMIT 1`, userID, reference).Scan(&provider, &calendarID, &accountEmail)
	return
}

func IsCalendarReference(reference string) bool {
	return strings.HasPrefix(reference, CalendarReferencePrefix)
}
