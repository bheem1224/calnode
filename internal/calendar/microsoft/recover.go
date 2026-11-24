package microsoft

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
	hc, err := c.clientForCalendar(ctx, user, reference)
	if err != nil {
		return "", err
	}
	if hc == nil {
		return "", errors.New("original Microsoft account is disconnected")
	}
	cal := reference
	if connstore.IsCalendarReference(reference) {
		_, cal, _, err = connstore.CalendarTarget(ctx, c.db, user, reference)
	}
	if err != nil {
		return "", err
	}
	endpoint := c.apiBase + "/me/events"
	if cal != "" && cal != "primary" {
		endpoint = c.apiBase + "/me/calendars/" + url.PathEscape(cal) + "/events"
	}
	q := url.Values{"$filter": {"singleValueExtendedProperties/Any(ep: ep/id eq '" + operationProperty + "' and ep/value eq '" + providerhttp.Key(user, operation) + "')"}, "$select": {"id"}, "$top": {"2"}}
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", providerhttp.Error(resp, "microsoft: recover event")
	}
	var out struct {
		Value []graphEventResp `json:"value"`
		Next  string           `json:"@odata.nextLink"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Value) > 1 || out.Next != "" {
		return "", errors.New("ambiguous Microsoft operation identity")
	}
	if len(out.Value) == 0 {
		return "", nil
	}
	if out.Value[0].ID == "" {
		return "", errors.New("incomplete Microsoft recovery response")
	}
	return out.Value[0].ID, nil
}
