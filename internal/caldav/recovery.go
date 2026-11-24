package caldav

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/calnode/calnode/internal/connstore"
	"github.com/calnode/calnode/internal/providerhttp"
)

func (c *Client) requestClient(rawURL, user string) *http.Client {
	u, _ := url.Parse(rawURL)
	origin := ""
	if u != nil {
		origin = u.Scheme + "://" + u.Host
	}
	app := providerhttp.Key("caldav", origin)
	return providerhttp.ForDB(c.db).Coordinate(c.hc, app, providerhttp.Key(app, user))
}

func (c *Client) createConn(ctx context.Context, user, reference, id string) (conn, bool, error) {
	if reference == "" {
		return c.loadConn(ctx, user, -1, 1)
	}
	collection := reference
	var account string
	if connstore.IsCalendarReference(reference) {
		provider, cal, email, err := connstore.CalendarTarget(ctx, c.db, user, reference)
		if err != nil {
			return conn{}, false, err
		}
		if provider != "caldav" {
			return conn{}, false, errors.New("caldav: provider mismatch")
		}
		collection, account = cal, email
	}
	cn, ok, err := c.eventConn(ctx, user, collection, joinURL(collection, id+".ics"))
	if err != nil || !ok {
		return cn, ok, err
	}
	if account != "" && cn.username != account {
		return conn{}, false, errors.New("caldav: calendar owner mismatch")
	}
	cn.calURL = collection
	return cn, true, nil
}

func hasOperationUID(body, id string) bool {
	count, matching := 0, 0
	for _, line := range unfold(body) {
		name, _, value := splitLine(line)
		if strings.EqualFold(name, "BEGIN") && strings.EqualFold(value, "VEVENT") {
			count++
		}
		if strings.EqualFold(name, "UID") && value == id+"@calnode" {
			matching++
		}
	}
	return count == 1 && matching == 1
}

func (c *Client) RecoverEvent(ctx context.Context, user, reference, key string) (string, error) {
	id := "calnode-" + providerhttp.Key(user, key)
	cn, ok, err := c.createConn(ctx, user, reference, id)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("original CalDAV account is disconnected")
	}
	resource := joinURL(cn.calURL, id+".ics")
	body, _, status, err := c.getICS(ctx, resource, cn.username, cn.password)
	if err != nil {
		return "", err
	}
	if status == 404 || status == 410 {
		return "", nil
	}
	if status != 200 {
		return "", errors.New("caldav: recovery lookup failed")
	}
	if !hasOperationUID(body, id) {
		return "", errors.New("caldav: recovery UID mismatch")
	}
	return resource, nil
}
