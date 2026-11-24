package gcal

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
	"github.com/calnode/calnode/internal/uid"
)

type calEventDateTime struct {
	DateTime string `json:"dateTime"`
	TimeZone string `json:"timeZone"`
}

type calEventAttendee struct {
	Email       string `json:"email"`
	DisplayName string `json:"displayName,omitempty"`
}

// conferenceData create-request: asks Google to allocate a Meet link for the event.
type calConferenceSolutionKey struct {
	Type string `json:"type"`
}
type calCreateConferenceRequest struct {
	RequestID             string                   `json:"requestId"`
	ConferenceSolutionKey calConferenceSolutionKey `json:"conferenceSolutionKey"`
}
type calConferenceData struct {
	CreateRequest *calCreateConferenceRequest `json:"createRequest,omitempty"`
}

type calEventReq struct {
	ID                 string             `json:"id,omitempty"`
	ExtendedProperties *calProperties     `json:"extendedProperties,omitempty"`
	Summary            string             `json:"summary"`
	Description        string             `json:"description,omitempty"`
	Location           string             `json:"location,omitempty"`
	Start              calEventDateTime   `json:"start"`
	End                calEventDateTime   `json:"end"`
	Attendees          []calEventAttendee `json:"attendees"`
	ConferenceData     *calConferenceData `json:"conferenceData,omitempty"`
	Visibility         string             `json:"visibility,omitempty"`
}

type calEntryPoint struct {
	EntryPointType string `json:"entryPointType"`
	URI            string `json:"uri"`
}

type calEventResp struct {
	Status             string         `json:"status"`
	ExtendedProperties *calProperties `json:"extendedProperties"`
	ID                 string         `json:"id"`
	HangoutLink        string         `json:"hangoutLink"`
	ConferenceData     *struct {
		EntryPoints []calEntryPoint `json:"entryPoints"`
	} `json:"conferenceData"`
}

type calProperties struct {
	Private map[string]string `json:"private"`
}

// meetLink extracts the Google Meet URL from a created event, preferring the
// top-level hangoutLink and falling back to the video conference entry point.
func (r calEventResp) meetLink() string {
	if r.HangoutLink != "" {
		return r.HangoutLink
	}
	if r.ConferenceData != nil {
		for _, ep := range r.ConferenceData.EntryPoints {
			if ep.EntryPointType == "video" && ep.URI != "" {
				return ep.URI
			}
		}
	}
	return ""
}

// CreateEvent creates a Google Calendar event and returns its event ID and, when
// p.AddMeet is set, the generated Google Meet URL. Returns ("", "", nil) if the
// user has no is_destination connection.
func (c *Client) CreateEvent(ctx context.Context, userID string, p calendar.CreateEventParams) (string, string, string, error) {
	hc, calID, err := c.clientForCalendar(ctx, userID, p.DestinationCalendarID)
	if err != nil || hc == nil {
		return "", "", "", err
	}
	if p.DestinationCalendarID != "" && !connstore.IsCalendarReference(p.DestinationCalendarID) {
		calID = p.DestinationCalendarID
	}

	attendees := []calEventAttendee{}
	if p.OrganizerEmail != "" {
		attendees = append(attendees, calEventAttendee{
			Email:       p.OrganizerEmail,
			DisplayName: p.OrganizerName,
		})
		for _, g := range p.Guests {
			if g != "" {
				attendees = append(attendees, calEventAttendee{Email: g})
			}
		}
	}

	reqBody := calEventReq{
		Summary:     p.Summary,
		Description: p.Description,
		Location:    p.Location,
		Start:       calEventDateTime{DateTime: p.Start.UTC().Format(time.RFC3339), TimeZone: "UTC"},
		End:         calEventDateTime{DateTime: p.End.UTC().Format(time.RFC3339), TimeZone: "UTC"},
		Attendees:   attendees,
	}
	if p.IdempotencyKey != "" {
		reqBody.ID = "c" + providerhttp.Key(userID, p.IdempotencyKey)
		reqBody.ExtendedProperties = &calProperties{map[string]string{"calnodeOperation": reqBody.ID}}
	}
	if p.Private {
		reqBody.Visibility = "private"
	}
	if p.AddMeet {
		reqBody.ConferenceData = &calConferenceData{
			CreateRequest: &calCreateConferenceRequest{
				RequestID:             uid.New(),
				ConferenceSolutionKey: calConferenceSolutionKey{Type: "hangoutsMeet"},
			},
		}
	}
	if p.AddMeet && reqBody.ID != "" {
		reqBody.ConferenceData.CreateRequest.RequestID = reqBody.ID
	}
	findExisting := func() (calEventResp, bool, error) {
		probe, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase+"/calendars/"+url.PathEscape(calID)+"/events/"+reqBody.ID, nil)
		resp, e := hc.Do(probe)
		if e != nil {
			return calEventResp{}, false, e
		}
		defer resp.Body.Close()
		if resp.StatusCode == 404 {
			return calEventResp{}, false, nil
		}
		if resp.StatusCode != 200 {
			return calEventResp{}, false, providerhttp.Error(resp, "gcal: find existing event")
		}
		var ev calEventResp
		if e = json.NewDecoder(resp.Body).Decode(&ev); e != nil {
			return ev, false, e
		}
		if ev.ID != reqBody.ID || ev.Status == "cancelled" || ev.ExtendedProperties == nil || ev.ExtendedProperties.Private["calnodeOperation"] != reqBody.ID {
			return ev, false, fmt.Errorf("gcal: event operation identity mismatch")
		}
		return ev, true, nil
	}
	returnExisting := func(ev calEventResp) (string, string, string, error) {
		if e := c.UpdateEvent(ctx, userID, p.DestinationCalendarID, ev.ID, p.Start, p.End); e != nil {
			return "", "", "", e
		}
		target := calID
		if connstore.IsCalendarReference(p.DestinationCalendarID) {
			target = p.DestinationCalendarID
		}
		return ev.ID, ev.meetLink(), target, nil
	}
	if reqBody.ID != "" {
		ev, found, e := findExisting()
		if e != nil {
			return "", "", "", e
		}
		if found {
			return returnExisting(ev)
		}
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", "", "", fmt.Errorf("gcal: create event marshal: %w", err)
	}

	apiURL := c.apiBase + "/calendars/" + url.PathEscape(calID) + "/events?sendUpdates=all"
	if p.AddMeet {
		apiURL += "&conferenceDataVersion=1" // required for conferenceData.createRequest to take effect
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return "", "", "", fmt.Errorf("gcal: create event request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if reqBody.ID != "" {
		req = providerhttp.ReplaySafe(req)
	}

	resp, err := hc.Do(req)
	if err != nil {
		return "", "", "", fmt.Errorf("gcal: create event call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 409 && reqBody.ID != "" {
		resp.Body.Close()
		ev, found, e := findExisting()
		if e != nil {
			return "", "", "", e
		}
		if !found {
			return "", "", "", fmt.Errorf("gcal: conflicting event could not be verified")
		}
		return returnExisting(ev)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", "", "", providerhttp.Error(resp, "gcal: create event")
	}

	var evResp calEventResp
	if err := json.NewDecoder(resp.Body).Decode(&evResp); err != nil {
		return "", "", "", fmt.Errorf("gcal: create event decode: %w", err)
	}
	if connstore.IsCalendarReference(p.DestinationCalendarID) {
		calID = p.DestinationCalendarID
	}
	return evResp.ID, evResp.meetLink(), calID, nil
}

// UpdateEvent moves an existing event to a new start/end (used on reschedule).
// Returns nil if eventID is empty or the user has no connection. sendUpdates=all
// so the attendee is notified of the new time.
func (c *Client) UpdateEvent(ctx context.Context, userID, calendarID, eventID string, start, end time.Time) error {
	start, end = start.Truncate(time.Second), end.Truncate(time.Second)
	return providerhttp.Serialize(ctx, providerhttp.Key(fmt.Sprintf("%p", c.db), "google", userID, calendarID, eventID), func() error { return c.updateEvent(ctx, userID, calendarID, eventID, start, end) })
}
func (c *Client) updateEvent(ctx context.Context, userID, calendarID, eventID string, start, end time.Time) error {
	if eventID == "" {
		return nil
	}
	hc, calID, err := c.clientForCalendar(ctx, userID, calendarID)
	if err != nil || hc == nil {
		return err
	}
	// Act on the calendar the event was created in, not wherever the destination points
	// now. Empty means the booking predates that being recorded, so fall back.
	//
	// Limitation: this only rescues a change of calendar WITHIN the connected account. If
	// the host moves their destination to a different account entirely, hc is that other
	// account's client and the old event is out of reach - correcting that needs the
	// account recorded too, which is a bigger change than the bug currently justifies.
	if calendarID != "" && !connstore.IsCalendarReference(calendarID) {
		calID = calendarID
	}
	probe, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase+"/calendars/"+url.PathEscape(calID)+"/events/"+url.PathEscape(eventID), nil)
	current, e := hc.Do(probe)
	if e != nil {
		return e
	}
	if current.StatusCode != 200 {
		err = providerhttp.Error(current, "gcal: read event before update")
		current.Body.Close()
		return err
	}
	var existing struct {
		Start, End calEventDateTime
		ETag       string `json:"etag"`
	}
	e = json.NewDecoder(current.Body).Decode(&existing)
	if existing.ETag == "" {
		existing.ETag = current.Header.Get("ETag")
	}
	current.Body.Close()
	if e != nil {
		return e
	}
	a, ae := time.Parse(time.RFC3339, existing.Start.DateTime)
	b, be := time.Parse(time.RFC3339, existing.End.DateTime)
	if ae != nil || be != nil {
		return fmt.Errorf("gcal: incomplete event times")
	}
	if a.Equal(start) && b.Equal(end) {
		return nil
	}

	body, err := json.Marshal(struct {
		Start calEventDateTime `json:"start"`
		End   calEventDateTime `json:"end"`
	}{
		Start: calEventDateTime{DateTime: start.UTC().Format(time.RFC3339), TimeZone: "UTC"},
		End:   calEventDateTime{DateTime: end.UTC().Format(time.RFC3339), TimeZone: "UTC"},
	})
	if err != nil {
		return fmt.Errorf("gcal: update event marshal: %w", err)
	}

	apiURL := c.apiBase + "/calendars/" + url.PathEscape(calID) + "/events/" + url.PathEscape(eventID) + "?sendUpdates=all"
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, apiURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("gcal: update event request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if existing.ETag != "" {
		req.Header.Set("If-Match", existing.ETag)
	}

	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("gcal: update event call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return providerhttp.Error(resp, "gcal: update event")
	}
	return nil
}

// CancelEvent deletes a Google Calendar event by its event ID.
// Returns nil if eventID is empty or the user has no connection.
func (c *Client) CancelEvent(ctx context.Context, userID, calendarID, eventID string) error {
	if eventID == "" {
		return nil
	}
	hc, calID, err := c.clientForCalendar(ctx, userID, calendarID)
	if err != nil || hc == nil {
		return err
	}
	// Act on the calendar the event was created in, not wherever the destination points
	// now. Empty means the booking predates that being recorded, so fall back.
	//
	// Limitation: this only rescues a change of calendar WITHIN the connected account. If
	// the host moves their destination to a different account entirely, hc is that other
	// account's client and the old event is out of reach - correcting that needs the
	// account recorded too, which is a bigger change than the bug currently justifies.
	if calendarID != "" && !connstore.IsCalendarReference(calendarID) {
		calID = calendarID
	}

	apiURL := c.apiBase + "/calendars/" + url.PathEscape(calID) + "/events/" + url.PathEscape(eventID) + "?sendUpdates=all"
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, apiURL, nil)
	if err != nil {
		return fmt.Errorf("gcal: cancel event request: %w", err)
	}

	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("gcal: cancel event call: %w", err)
	}
	defer resp.Body.Close()

	// 204 No Content = deleted; 410 Gone = already deleted — both are fine.
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusGone && resp.StatusCode != http.StatusNotFound {
		return providerhttp.Error(resp, "gcal: cancel event")
	}
	return nil
}
