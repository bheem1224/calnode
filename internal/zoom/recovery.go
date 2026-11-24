package zoom

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/calnode/calnode/internal/providerhttp"
)

var ErrUncertainCreation = errors.New("Zoom creation outcome is uncertain; reconciliation will not send another create")
var ErrRetiredMeeting = errors.New("Zoom meeting belongs to a cancelled booking")

type meetingRecord struct {
	ID       json.Number `json:"id"`
	Agenda   string      `json:"agenda"`
	Topic    string      `json:"topic"`
	JoinURL  string      `json:"join_url"`
	Start    string      `json:"start_time"`
	Duration int         `json:"duration"`
}
type meetingIntent struct {
	key, booking, user, marker, topic, state, id, join string
	cleanup, done                                      int
}

func (c *Client) operationLock(key string) string {
	return providerhttp.Key("zoom-lifecycle", fmt.Sprintf("%p", c.db), key)
}
func (c *Client) intent(ctx context.Context, key string) (v meetingIntent, err error) {
	err = c.db.QueryRowContext(ctx, `SELECT operation_key,booking_id,user_id,marker,topic,state,meeting_id,join_url,cleanup_requested,cleanup_done FROM zoom_meeting_intents WHERE operation_key=?`, key).Scan(&v.key, &v.booking, &v.user, &v.marker, &v.topic, &v.state, &v.id, &v.join, &v.cleanup, &v.done)
	return
}

// Zoom does not offer a documented idempotency contract for Create Meeting.
// Persist the attempt before bytes can be sent. An uncertain outcome is recovered
// by a unique agenda marker; absence never authorizes another automatic POST.
func (c *Client) CreateMeeting(ctx context.Context, user string, p MeetingParams) (join, id string, err error) {
	if p.OperationKey == "" {
		return c.createMeeting(ctx, user, p)
	}
	err = providerhttp.Serialize(ctx, c.operationLock(p.OperationKey), func() error {
		marker := "Calnode operation " + providerhttp.Key(user, p.OperationKey)
		if _, err := c.db.ExecContext(ctx, `INSERT OR IGNORE INTO zoom_meeting_intents(operation_key,booking_id,user_id,marker,topic)VALUES(?,?,?,?,?)`, p.OperationKey, p.BookingID, user, marker, p.Topic); err != nil {
			return err
		}
		v, err := c.intent(ctx, p.OperationKey)
		if err != nil {
			return err
		}
		if v.booking != "" {
			var live int
			if err = c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookings WHERE id=? AND status='confirmed'`, v.booking).Scan(&live); err != nil {
				return err
			}
			if live != 1 {
				_, err = c.db.ExecContext(ctx, `UPDATE zoom_meeting_intents SET cleanup_requested=1,next_attempt_at='' WHERE operation_key=?`, v.key)
				if err != nil {
					return err
				}
				return ErrRetiredMeeting
			}
		}
		if v.cleanup != 0 {
			return ErrRetiredMeeting
		}
		if v.state == "ready" {
			join, id = v.join, v.id
			return nil
		}
		if v.state == "uncertain" {
			found, err := c.findMeeting(ctx, v)
			if err != nil {
				return err
			}
			if found.ID == "" {
				return ErrUncertainCreation
			}
			join, id = found.JoinURL, found.ID.String()
			if join == "" {
				return errors.New("Zoom recovery omitted join URL")
			}
		} else {
			p.Topic = v.topic
			p.agenda = v.marker
			p.beforeSend = func(send context.Context) error {
				result, err := c.db.ExecContext(send, `UPDATE zoom_meeting_intents SET state='uncertain',attempts=attempts+1 WHERE operation_key=? AND state='new' AND cleanup_requested=0`, v.key)
				if err != nil {
					return err
				}
				if n, _ := result.RowsAffected(); n != 1 {
					return ErrRetiredMeeting
				}
				return nil
			}
			join, id, err = c.createMeeting(ctx, v.user, p)
			if err != nil {
				var rejected *providerhttp.HTTPError
				if errors.As(err, &rejected) && rejected.Status >= 400 && rejected.Status < 500 && rejected.Status != 408 && rejected.Status != 409 {
					_, _ = c.db.ExecContext(context.WithoutCancel(ctx), `UPDATE zoom_meeting_intents SET state='new' WHERE operation_key=? AND cleanup_requested=0`, v.key)
				}
				return err
			}
			if id == "" || join == "" {
				return errors.New("Zoom creation acknowledgement incomplete")
			}
		}
		persist, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err = c.db.ExecContext(persist, `UPDATE zoom_meeting_intents SET state='ready',meeting_id=?,join_url=?,last_error='' WHERE operation_key=?`, id, join, v.key); err != nil {
			return err
		}
		v, err = c.intent(persist, v.key)
		if err != nil {
			return err
		}
		if v.cleanup != 0 {
			return ErrRetiredMeeting
		}
		return nil
	})
	return
}

func (c *Client) getMeeting(ctx context.Context, user, id string) (out meetingRecord, err error) {
	hc, err := c.authedClient(ctx, user)
	if err != nil {
		return out, err
	}
	if hc == nil {
		return out, errors.New("original Zoom account needs reconnection")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", c.apiBase+"/meetings/"+url.PathEscape(id), nil)
	if err != nil {
		return out, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return out, providerhttp.Error(resp, "zoom: read meeting")
	}
	err = json.NewDecoder(resp.Body).Decode(&out)
	return
}

func (c *Client) findMeeting(ctx context.Context, v meetingIntent) (found meetingRecord, err error) {
	hc, err := c.authedClient(ctx, v.user)
	if err != nil {
		return found, err
	}
	if hc == nil {
		return found, errors.New("original Zoom account needs reconnection")
	}
	page := ""
	seen := map[string]bool{}
	for {
		if seen[page] || len(seen) >= 100 {
			return found, errors.New("invalid Zoom meeting pagination")
		}
		seen[page] = true
		q := url.Values{"type": {"scheduled"}, "page_size": {"300"}}
		if page != "" {
			q.Set("next_page_token", page)
		}
		req, err := http.NewRequestWithContext(ctx, "GET", c.apiBase+"/users/me/meetings?"+q.Encode(), nil)
		if err != nil {
			return found, err
		}
		resp, err := hc.Do(req)
		if err != nil {
			return found, err
		}
		if resp.StatusCode != 200 {
			failure := providerhttp.Error(resp, "zoom: recover meeting")
			resp.Body.Close()
			return found, failure
		}
		var listing struct {
			Meetings []meetingRecord `json:"meetings"`
			Next     string          `json:"next_page_token"`
		}
		err = json.NewDecoder(resp.Body).Decode(&listing)
		resp.Body.Close()
		if err != nil {
			return found, err
		}
		for _, m := range listing.Meetings {
			if m.Agenda == "" && m.Topic == v.topic {
				m, err = c.getMeeting(ctx, v.user, m.ID.String())
				if err != nil {
					return found, err
				}
			}
			if m.Agenda != v.marker {
				continue
			}
			if m.ID == "" {
				return found, errors.New("Zoom recovery omitted meeting ID")
			}
			if found.ID != "" && found.ID != m.ID {
				return found, errors.New("multiple Zoom meetings have the same operation marker")
			}
			if m.JoinURL == "" {
				m, err = c.getMeeting(ctx, v.user, m.ID.String())
				if err != nil {
					return found, err
				}
				if m.Agenda != v.marker {
					return found, errors.New("Zoom recovery marker mismatch")
				}
			}
			found = m
		}
		page = listing.Next
		if page == "" {
			return found, nil
		}
	}
}

// ReconcileCleanup uses the original creator even after a host reassignment.
// It also reconciles reschedule drift for ready meetings on confirmed bookings.
func (c *Client) ReconcileCleanup(ctx context.Context) error {
	return c.reconcile(ctx, "")
}

func (c *Client) CleanupBooking(ctx context.Context, booking string) error {
	// Preserve cancellation of legacy meetings created before the journal existed.
	if _, err := c.db.ExecContext(ctx, `INSERT OR IGNORE INTO zoom_meeting_intents(operation_key,booking_id,user_id,marker,topic,state,meeting_id,join_url,cleanup_requested) SELECT 'booking:'||id,id,host_id,'','','ready',zoom_meeting_id,COALESCE(location_value,''),1 FROM bookings WHERE id=? AND status='cancelled' AND COALESCE(zoom_meeting_id,'')<>''`, booking); err != nil {
		return err
	}
	return c.reconcile(ctx, booking)
}

func (c *Client) reconcile(ctx context.Context, booking string) error {
	rows, err := c.db.QueryContext(ctx, `SELECT operation_key FROM zoom_meeting_intents WHERE next_attempt_at<=? AND (?='' OR booking_id=?) AND ((cleanup_requested=1 AND cleanup_done=0) OR (state IN('ready','uncertain') AND booking_id IN(SELECT id FROM bookings WHERE status='confirmed' AND end_at>?))) ORDER BY next_attempt_at,operation_key LIMIT 100`, time.Now().UTC().Format(time.RFC3339Nano), booking, booking, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	var keys []string
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var failures []error
	for _, key := range keys {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err = providerhttp.Serialize(ctx, c.operationLock(key), func() error {
			v, err := c.intent(ctx, key)
			if err != nil {
				return err
			}
			if v.cleanup != 0 && v.done == 0 {
				if v.id == "" && v.state == "uncertain" {
					m, err := c.findMeeting(ctx, v)
					if err != nil {
						return err
					}
					v.id = m.ID.String()
					if v.id == "" {
						var created string
						if err = c.db.QueryRowContext(ctx, `SELECT created_at FROM zoom_meeting_intents WHERE operation_key=?`, key).Scan(&created); err != nil {
							return err
						}
						at, _ := time.Parse(time.RFC3339Nano, created)
						if at.IsZero() || time.Since(at) < 72*time.Hour {
							return ErrUncertainCreation
						}
					}
					if _, err = c.db.ExecContext(ctx, `UPDATE zoom_meeting_intents SET meeting_id=? WHERE operation_key=?`, v.id, key); err != nil {
						return err
					}
				}
				if v.id != "" {
					if err = c.DeleteMeeting(ctx, v.user, v.id); err != nil {
						return err
					}
				}
				_, err = c.db.ExecContext(ctx, `UPDATE zoom_meeting_intents SET cleanup_done=1,last_error='' WHERE operation_key=?`, key)
				return err
			}
			if v.state == "uncertain" && v.cleanup == 0 && v.booking != "" {
				m, err := c.findMeeting(ctx, v)
				if err != nil {
					return err
				}
				if m.ID == "" || m.JoinURL == "" {
					return ErrUncertainCreation
				}
				tx, err := c.db.BeginTx(ctx, nil)
				if err != nil {
					return err
				}
				defer tx.Rollback()
				if _, err = tx.ExecContext(ctx, `UPDATE zoom_meeting_intents SET state='ready',meeting_id=?,join_url=?,last_error='' WHERE operation_key=?`, m.ID.String(), m.JoinURL, key); err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, `UPDATE bookings SET zoom_meeting_id=?,location_value=? WHERE id=? AND status='confirmed'`, m.ID.String(), m.JoinURL, v.booking); err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, `UPDATE jobs SET status='pending',attempts=0,run_at=?,last_error=NULL,locked_until=NULL,finished_at=NULL WHERE id=? AND status='failed'`, time.Now().UTC().Format(time.RFC3339Nano), "booking.confirm:"+v.booking); err != nil {
					return err
				}
				if err = tx.Commit(); err != nil {
					return err
				}
				v.state, v.id, v.join = "ready", m.ID.String(), m.JoinURL
			}
			if v.state == "ready" && v.booking != "" {
				var start, end string
				if err = c.db.QueryRowContext(ctx, `SELECT start_at,end_at FROM bookings WHERE id=? AND status='confirmed'`, v.booking).Scan(&start, &end); err != nil {
					return err
				}
				a, ae := time.Parse(time.RFC3339Nano, start)
				b, be := time.Parse(time.RFC3339Nano, end)
				if ae != nil || be != nil {
					return errors.New("invalid Zoom booking time")
				}
				if err = c.UpdateMeeting(ctx, v.user, v.id, a, int(b.Sub(a).Minutes()), ""); err != nil {
					return err
				}
			}
			_, err = c.db.ExecContext(ctx, `UPDATE zoom_meeting_intents SET next_attempt_at=?,last_error='' WHERE operation_key=?`, time.Now().Add(10*time.Minute).UTC().Format(time.RFC3339Nano), key)
			return err
		})
		if err != nil {
			_, _ = c.db.ExecContext(ctx, `UPDATE zoom_meeting_intents SET next_attempt_at=?,last_error='Zoom reconciliation pending; check provider access or uncertain creation' WHERE operation_key=?`, time.Now().Add(5*time.Minute).UTC().Format(time.RFC3339Nano), key)
			failures = append(failures, fmt.Errorf("Zoom operation %s: %w", key, err))
		}
	}
	return errors.Join(failures...)
}

func (c *Client) BookingOwner(ctx context.Context, booking, fallback string) (string, error) {
	var user string
	err := c.db.QueryRowContext(ctx, `SELECT user_id FROM zoom_meeting_intents WHERE booking_id=? LIMIT 1`, booking).Scan(&user)
	if errors.Is(err, sql.ErrNoRows) {
		return fallback, nil
	}
	return user, err
}
