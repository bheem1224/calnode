package microsoft

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/connstore"
	"github.com/calnode/calnode/internal/providerhttp"
)

// graphTZ is the UTC wall-clock format Graph expects for event start/end.
const graphTZ = "2006-01-02T15:04:05"

type graphAttendee struct {
	EmailAddress struct {
		Address string `json:"address"`
		Name    string `json:"name,omitempty"`
	} `json:"emailAddress"`
	Type string `json:"type"`
}

type graphLocation struct {
	DisplayName string `json:"displayName"`
}

type graphEventReq struct {
	TransactionID         string          `json:"transactionId,omitempty"`
	Properties            []graphProperty `json:"singleValueExtendedProperties,omitempty"`
	Subject               string          `json:"subject"`
	Body                  *graphItemBody  `json:"body,omitempty"`
	Start                 graphDateTime   `json:"start"`
	End                   graphDateTime   `json:"end"`
	Location              *graphLocation  `json:"location,omitempty"`
	Attendees             []graphAttendee `json:"attendees,omitempty"`
	IsOnlineMeeting       bool            `json:"isOnlineMeeting,omitempty"`
	OnlineMeetingProvider string          `json:"onlineMeetingProvider,omitempty"`
	Sensitivity           string          `json:"sensitivity,omitempty"`
}

type graphItemBody struct {
	ContentType string `json:"contentType"`
	Content     string `json:"content"`
}

type graphEventResp struct {
	ID            string `json:"id"`
	OnlineMeeting *struct {
		JoinURL string `json:"joinUrl"`
	} `json:"onlineMeeting"`
}

type graphProperty struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

const operationProperty = "String {10c4a498-f01f-4f30-a4f7-e7429c827ac5} Name CalnodeOperation"

// CreateEvent creates a Graph event on the user's default calendar and returns its
// ID and, when p.AddMeet is set, the Teams join URL. Graph emails attendees its own
// invite. Returns ("", "", nil) if the user has no is_destination connection.
func (c *Client) CreateEvent(ctx context.Context, userID string, p calendar.CreateEventParams) (string, string, string, error) {
	hc, err := c.clientForCalendar(ctx, userID, p.DestinationCalendarID)
	if err != nil || hc == nil {
		return "", "", "", err
	}

	reqBody := graphEventReq{
		Subject: p.Summary,
		Start:   graphDateTime{DateTime: p.Start.UTC().Format(graphTZ), TimeZone: "UTC"},
		End:     graphDateTime{DateTime: p.End.UTC().Format(graphTZ), TimeZone: "UTC"},
	}
	if p.IdempotencyKey != "" {
		reqBody.TransactionID = providerhttp.Key(userID, p.IdempotencyKey)
		reqBody.Properties = []graphProperty{{operationProperty, reqBody.TransactionID}}
	}
	if p.Description != "" {
		reqBody.Body = &graphItemBody{ContentType: "text", Content: p.Description}
	}
	if p.Location != "" {
		reqBody.Location = &graphLocation{DisplayName: p.Location}
	}
	if p.OrganizerEmail != "" {
		a := graphAttendee{Type: "required"}
		a.EmailAddress.Address = p.OrganizerEmail
		a.EmailAddress.Name = p.OrganizerName
		reqBody.Attendees = append(reqBody.Attendees, a)
		for _, g := range p.Guests {
			if g != "" {
				ga := graphAttendee{Type: "optional"}
				ga.EmailAddress.Address = g
				reqBody.Attendees = append(reqBody.Attendees, ga)
			}
		}
	}
	if p.AddMeet {
		reqBody.IsOnlineMeeting = true
		reqBody.OnlineMeetingProvider = "teamsForBusiness"
	}
	if p.Private {
		reqBody.Sensitivity = "private"
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", "", "", fmt.Errorf("microsoft: create event marshal: %w", err)
	}
	// Default calendar unless the user picked a specific one inside the account. Update and
	// cancel stay on /me/events/{id}, which Graph resolves across all of the user's
	// calendars, so only creation needs to be calendar-scoped.
	createURL := c.apiBase + "/me/events"
	destCal := p.DestinationCalendarID
	if connstore.IsCalendarReference(destCal) {
		_, destCal, _, err = connstore.CalendarTarget(ctx, c.db, userID, destCal)
	}
	if destCal == "" {
		destCal, err = c.destinationCalendarID(ctx, userID)
	}
	if err != nil {
		return "", "", "", err
	}
	if destCal != "" && destCal != "primary" {
		createURL = c.apiBase + "/me/calendars/" + url.PathEscape(destCal) + "/events"
	}
	if p.IdempotencyKey != "" {
		q := url.Values{"$filter": {"singleValueExtendedProperties/Any(ep: ep/id eq '" + operationProperty + "' and ep/value eq '" + reqBody.TransactionID + "')"}, "$select": {"id,onlineMeeting"}, "$top": {"2"}}
		probe, _ := http.NewRequestWithContext(ctx, http.MethodGet, createURL+"?"+q.Encode(), nil)
		resp, e := hc.Do(probe)
		if e != nil {
			return "", "", "", e
		}
		if resp.StatusCode != 200 {
			err = providerhttp.Error(resp, "microsoft: find existing event")
			resp.Body.Close()
			return "", "", "", err
		}
		var found struct {
			Value []graphEventResp `json:"value"`
			Next  string           `json:"@odata.nextLink"`
		}
		e = json.NewDecoder(resp.Body).Decode(&found)
		resp.Body.Close()
		if e != nil {
			return "", "", "", e
		}
		if len(found.Value) > 1 || found.Next != "" {
			return "", "", "", fmt.Errorf("microsoft: ambiguous event operation")
		}
		if len(found.Value) == 1 {
			ev := found.Value[0]
			if ev.ID == "" {
				return "", "", "", fmt.Errorf("microsoft: incomplete existing event")
			}
			if e = c.UpdateEvent(ctx, userID, p.DestinationCalendarID, ev.ID, p.Start, p.End); e != nil {
				return "", "", "", e
			}
			join := ""
			if ev.OnlineMeeting != nil {
				join = ev.OnlineMeeting.JoinURL
			}
			return ev.ID, join, p.DestinationCalendarID, nil
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, createURL, bytes.NewReader(body))
	if err != nil {
		return "", "", "", fmt.Errorf("microsoft: create event request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if p.IdempotencyKey != "" {
		req = providerhttp.ReplaySafe(req)
	}

	resp, err := hc.Do(req)
	if err != nil {
		return "", "", "", fmt.Errorf("microsoft: create event call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", "", "", providerhttp.Error(resp, "microsoft: create event")
	}

	var evResp graphEventResp
	if err := json.NewDecoder(resp.Body).Decode(&evResp); err != nil {
		return "", "", "", fmt.Errorf("microsoft: create event decode: %w", err)
	}
	join := ""
	if evResp.OnlineMeeting != nil {
		join = evResp.OnlineMeeting.JoinURL
	}
	if connstore.IsCalendarReference(p.DestinationCalendarID) {
		destCal = p.DestinationCalendarID
	}
	return evResp.ID, join, destCal, nil
}

// UpdateEvent moves an existing event to a new start/end (reschedule). Graph
// notifies attendees. No-op if eventID is empty or the user has no connection.
//
// calendarID is accepted for interface parity but deliberately unused: Graph addresses an
// event by id at /me/events/{id} regardless of which of the user's calendars holds it, so
// unlike Google and CalDAV there is nothing to re-target.
func (c *Client) UpdateEvent(ctx context.Context, userID, calendarID, eventID string, start, end time.Time) error {
	start, end = start.Truncate(time.Second), end.Truncate(time.Second) // match encoded provider precision
	return providerhttp.Serialize(ctx, providerhttp.Key(fmt.Sprintf("%p", c.db), "microsoft", userID, calendarID, eventID), func() error { return c.updateEvent(ctx, userID, calendarID, eventID, start, end) })
}
func (c *Client) updateEvent(ctx context.Context, userID, calendarID, eventID string, start, end time.Time) error {
	if eventID == "" {
		return nil
	}
	hc, err := c.clientForCalendar(ctx, userID, calendarID)
	if err != nil || hc == nil {
		return err
	}
	// A preceding PATCH may have succeeded even if its response was lost. Read
	// current state before another write, because repeating PATCH may email again.
	probe, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase+"/me/events/"+url.PathEscape(eventID)+"?$select=start,end", nil)
	probe.Header.Set("Prefer", `outlook.timezone="UTC"`)
	current, e := hc.Do(probe)
	if e != nil {
		return e
	}
	if current.StatusCode != 200 {
		err = providerhttp.Error(current, "microsoft: read event before update")
		current.Body.Close()
		return err
	}
	var existing struct {
		Start, End graphDateTime
		ETag       string `json:"@odata.etag"`
	}
	e = json.NewDecoder(current.Body).Decode(&existing)
	if existing.ETag == "" {
		existing.ETag = current.Header.Get("ETag")
	}
	current.Body.Close()
	if e != nil {
		return e
	}
	a, ae := existing.Start.parse()
	b, be := existing.End.parse()
	if ae != nil || be != nil {
		return fmt.Errorf("microsoft: incomplete event times")
	}
	if a.Equal(start) && b.Equal(end) {
		return nil
	}

	body, err := json.Marshal(struct {
		Start graphDateTime `json:"start"`
		End   graphDateTime `json:"end"`
	}{
		Start: graphDateTime{DateTime: start.UTC().Format(graphTZ), TimeZone: "UTC"},
		End:   graphDateTime{DateTime: end.UTC().Format(graphTZ), TimeZone: "UTC"},
	})
	if err != nil {
		return fmt.Errorf("microsoft: update event marshal: %w", err)
	}
	apiURL := c.apiBase + "/me/events/" + url.PathEscape(eventID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, apiURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("microsoft: update event request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if existing.ETag != "" {
		req.Header.Set("If-Match", existing.ETag)
	}

	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("microsoft: update event call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return providerhttp.Error(resp, "microsoft: update event")
	}
	return nil
}

// CancelEvent deletes a Graph event by ID (Graph notifies attendees). No-op if
// eventID is empty or the user has no connection.
// calendarID is unused here for the same reason as UpdateEvent: /me/events/{id} resolves
// across all of the user's calendars.
func (c *Client) CancelEvent(ctx context.Context, userID, calendarID, eventID string) error {
	if eventID == "" {
		return nil
	}
	hc, err := c.clientForCalendar(ctx, userID, calendarID)
	if err != nil || hc == nil {
		return err
	}
	apiURL := c.apiBase + "/me/events/" + url.PathEscape(eventID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, apiURL, nil)
	if err != nil {
		return fmt.Errorf("microsoft: cancel event request: %w", err)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("microsoft: cancel event call: %w", err)
	}
	defer resp.Body.Close()
	// 204 = deleted; 404 = already gone — both fine.
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		return providerhttp.Error(resp, "microsoft: cancel event")
	}
	return nil
}
