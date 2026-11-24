package gcal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/calnode/calnode/internal/connstore"
	"github.com/calnode/calnode/internal/providerhttp"
)

func (c *Client) RecoverEvent(ctx context.Context, user, reference, operation string) (string, error) {
	hc, cal, err := c.clientForCalendar(ctx, user, reference)
	if err != nil {
		return "", err
	}
	if hc == nil {
		return "", errors.New("original Google account is disconnected")
	}
	if reference != "" && !connstore.IsCalendarReference(reference) {
		cal = reference
	}
	id := "c" + providerhttp.Key(user, operation)
	req, err := http.NewRequestWithContext(ctx, "GET", c.apiBase+"/calendars/"+url.PathEscape(cal)+"/events/"+id, nil)
	if err != nil {
		return "", err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 || resp.StatusCode == 410 {
		return "", nil
	}
	if resp.StatusCode != 200 {
		return "", providerhttp.Error(resp, "gcal: recover event")
	}
	var ev calEventResp
	if err = json.NewDecoder(resp.Body).Decode(&ev); err != nil {
		return "", err
	}
	if ev.Status == "cancelled" {
		return "", nil
	}
	if ev.ID != id || ev.ExtendedProperties == nil || ev.ExtendedProperties.Private["calnodeOperation"] != id {
		return "", errors.New("Google operation identity mismatch")
	}
	return id, nil
}
