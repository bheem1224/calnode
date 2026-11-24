package gcal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/calnode/calnode/internal/providerhttp"
	"github.com/calnode/calnode/internal/slots"
)

type freeBusyReq struct {
	TimeMin string              `json:"timeMin"`
	TimeMax string              `json:"timeMax"`
	Items   []map[string]string `json:"items"`
}

type freeBusyResp struct {
	Calendars map[string]struct {
		Errors []json.RawMessage `json:"errors"`
		Busy   []struct {
			Start string `json:"start"`
			End   string `json:"end"`
		} `json:"busy"`
	} `json:"calendars"`
}

func (c *Client) FreeBusy(ctx context.Context, userID string, from, to time.Time) ([]slots.Interval, error) {
	conns, err := c.freeBusyConnections(ctx, userID)
	if err != nil {
		return nil, err
	}
	var out []slots.Interval
	for _, conn := range conns {
		intervals, err := c.freeBusyForConn(ctx, conn, from, to)
		if err != nil {
			return nil, err
		}
		out = append(out, intervals...)
	}
	return out, nil
}

// freeBusyForConn queries one connection's selected calendars for busy intervals in [from, to).
// Google's freeBusy accepts multiple items and returns each calendar's busy blocks, which the
// decoder below unions.
func (c *Client) freeBusyForConn(ctx context.Context, conn fbConn, from, to time.Time) ([]slots.Interval, error) {
	items := make([]map[string]string, 0, len(conn.calIDs))
	for _, id := range conn.calIDs {
		items = append(items, map[string]string{"id": id})
	}
	body, err := json.Marshal(freeBusyReq{
		TimeMin: from.UTC().Format(time.RFC3339),
		TimeMax: to.UTC().Format(time.RFC3339),
		Items:   items,
	})
	if err != nil {
		return nil, fmt.Errorf("gcal: freeBusy marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiBase+"/freeBusy", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gcal: freeBusy request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req = providerhttp.ReplaySafe(req)

	resp, err := conn.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gcal: freeBusy call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, providerhttp.Error(resp, "gcal: freeBusy")
	}

	var fbr freeBusyResp
	if err := json.NewDecoder(resp.Body).Decode(&fbr); err != nil {
		return nil, fmt.Errorf("gcal: freeBusy decode: %w", err)
	}
	// A fallback makes another request on this account. Release the first
	// response's concurrency slot before that request can wait for one.
	resp.Body.Close()
	var out []slots.Interval
	for _, id := range conn.calIDs {
		cal, ok := fbr.Calendars[id]
		if !ok {
			return nil, fmt.Errorf("gcal: calendar busy status unavailable")
		}
		if len(cal.Errors) > 0 {
			fallback, err := c.calendarEventsBusy(ctx, conn.hc, id, from, to)
			if err != nil {
				return nil, fmt.Errorf("gcal: selected calendar cannot be checked (check access or reconnect): %w", err)
			}
			out = append(out, fallback...)
			continue
		}
		for _, b := range cal.Busy {
			s, err1 := time.Parse(time.RFC3339, b.Start)
			e, err2 := time.Parse(time.RFC3339, b.End)
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("gcal: invalid busy interval")
			}
			out = append(out, slots.Interval{Start: s, End: e})
		}
	}
	return out, nil
}

// Some subscribed calendars (notably holidays) can be listed/read but reject FreeBusy.
// Read expanded event instances instead, never treating a failed lookup as free time.
func (c *Client) calendarEventsBusy(ctx context.Context, hc *http.Client, id string, from, to time.Time) ([]slots.Interval, error) {
	out := []slots.Interval{}
	page := ""
	seen := map[string]bool{}
	for {
		q := url.Values{"timeMin": {from.UTC().Format(time.RFC3339)}, "timeMax": {to.UTC().Format(time.RFC3339)}, "singleEvents": {"true"}, "maxResults": {"2500"}}
		if page != "" {
			q.Set("pageToken", page)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase+"/calendars/"+url.PathEscape(id)+"/events?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		resp, err := hc.Do(req)
		if err != nil {
			return nil, err
		}
		var result struct {
			Kind, TimeZone, NextPageToken string
			Items                         *[]struct {
				Status, Transparency string
				Start, End           struct{ DateTime, Date string }
			}
		}
		if resp.StatusCode != 200 {
			failure := providerhttp.Error(resp, "gcal: events lookup")
			resp.Body.Close()
			return nil, failure
		}
		err = json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if result.Kind != "calendar#events" && result.Items == nil {
			return nil, fmt.Errorf("incomplete calendar events response")
		}
		if result.Items != nil {
			for _, e := range *result.Items {
				if e.Status == "cancelled" || e.Transparency == "transparent" {
					continue
				}
				start, err1 := time.Parse(time.RFC3339, e.Start.DateTime)
				end, err2 := time.Parse(time.RFC3339, e.End.DateTime)
				if e.Start.Date != "" && e.End.Date != "" {
					zone, err := time.LoadLocation(result.TimeZone)
					if err != nil || result.TimeZone == "" {
						return nil, fmt.Errorf("missing calendar timezone")
					}
					start, err1 = time.ParseInLocation("2006-01-02", e.Start.Date, zone)
					end, err2 = time.ParseInLocation("2006-01-02", e.End.Date, zone)
				}
				if err1 != nil || err2 != nil || !end.After(start) {
					return nil, fmt.Errorf("invalid calendar event interval")
				}
				out = append(out, slots.Interval{Start: start, End: end})
			}
		}
		page = result.NextPageToken
		if page == "" {
			return out, nil
		}
		if seen[page] || len(seen) >= 1000 {
			return nil, fmt.Errorf("repeated calendar page token")
		}
		seen[page] = true
	}
}
