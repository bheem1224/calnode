package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/mailer"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestApprovalEnforcedAcrossBookingTransports(t *testing.T) {
	for _, transport := range []string{"REST", "MCP"} {
		t.Run(transport, func(t *testing.T) {
			h, database, key, owner := setupWorkspaceWithDB(t)
			slug, id := seedEventTypeHTTP(t, h, key)
			seedFullAvailabilityDB(t, database, owner)
			mustExec(t, database, `UPDATE event_types SET requires_confirmation=1,confirm_mode='always' WHERE id=?`, id)
			var result struct {
				ID, Status string
				Pending    bool `json:"pending_approval"`
			}
			start := futureAt(3, 10, 0).Format(time.RFC3339)
			if transport == "REST" {
				body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":%q,"name":"Booker","email":"booker@example.test"}`, slug, start)
				rec := httptest.NewRecorder()
				h.CreateBooking(rec, httptest.NewRequest("POST", "/v1/bookings", strings.NewReader(body)))
				if rec.Code != 201 {
					t.Fatal(rec.Code, rec.Body.String())
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
			} else {
				cs := connectMCP(t, h)
				res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_booking", Arguments: map[string]any{"event_type_id": slug, "slot_start": start, "attendee_name": "Booker", "attendee_email": "booker@example.test"}})
				if err != nil || res.IsError {
					t.Fatalf("%v %+v", err, res)
				}
				raw, _ := json.Marshal(res.StructuredContent)
				if err := json.Unmarshal(raw, &result); err != nil {
					t.Fatal(err)
				}
			}
			if result.Status != "pending" || !result.Pending {
				t.Fatalf("approval bypassed: %+v", result)
			}
			var jobs int
			if err := database.QueryRow(`SELECT count(*) FROM jobs WHERE type='booking.confirm' AND json_extract(payload,'$.booking_id')=?`, result.ID).Scan(&jobs); err != nil || jobs != 0 {
				t.Fatalf("pending booking dispatched: jobs=%d err=%v", jobs, err)
			}
		})
	}
}

func TestRescheduleKeepsActualDurationAcrossTransports(t *testing.T) {
	for _, transport := range []string{"REST", "MCP"} {
		t.Run(transport, func(t *testing.T) {
			h, database, key, owner := setupWorkspaceWithDB(t)
			slug, _ := seedEventTypeHTTP(t, h, key)
			seedFullAvailabilityDB(t, database, owner)
			start := futureAt(4, 10, 0)
			id := createBookingViaHTTP(t, h, slug, start.Format(time.RFC3339))
			mustExec(t, database, `UPDATE bookings SET end_at=? WHERE id=?`, start.Add(15*time.Minute).Format(time.RFC3339Nano), id)
			next := futureAt(5, 10, 0)
			var result struct {
				Start string `json:"start_at"`
				End   string `json:"end_at"`
			}
			if transport == "REST" {
				rec := patchReschedule(t, h, id, next.Format(time.RFC3339), key)
				if rec.Code != 200 {
					t.Fatal(rec.Code, rec.Body.String())
				}
				json.Unmarshal(rec.Body.Bytes(), &result)
			} else {
				cs := connectMCP(t, h)
				res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "reschedule_booking", Arguments: map[string]any{"booking_id": id, "new_slot_start": next.Format(time.RFC3339)}})
				if err != nil || res.IsError {
					t.Fatalf("%v %+v", err, res)
				}
				raw, _ := json.Marshal(res.StructuredContent)
				json.Unmarshal(raw, &result)
			}
			from, _ := time.Parse(time.RFC3339, result.Start)
			to, _ := time.Parse(time.RFC3339, result.End)
			if to.Sub(from) != 15*time.Minute {
				t.Fatalf("duration changed: %+v", result)
			}
		})
	}
}
func TestInstanceAdminCanRescheduleAnotherHost(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	slug, id := seedEventTypeHTTP(t, h, key)
	mustExec(t, database, `INSERT INTO users(id,email,name,iana_timezone)VALUES('other-host','other@example.test','Other','UTC')`)
	seedFullAvailabilityDB(t, database, "other-host")
	mustExec(t, database, `UPDATE event_type_hosts SET user_id='other-host' WHERE event_type_id=?`, id)
	bookingID := createBookingViaHTTP(t, h, slug, futureAt(4, 10, 0).Format(time.RFC3339))
	rec := patchReschedule(t, h, bookingID, futureAt(5, 10, 0).Format(time.RFC3339), key)
	if rec.Code != 200 {
		t.Fatalf("admin denied: %d %s", rec.Code, rec.Body.String())
	}
}
func TestCheckoutRequiresDurablePaidState(t *testing.T) {
	h, database, key, owner := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	seedFullAvailabilityDB(t, database, owner)
	id := createBookingViaHTTP(t, h, slug, futureAt(4, 10, 0).Format(time.RFC3339))
	for _, tc := range []struct {
		state, slug, session string
		status               int
	}{{"pending", slug, "cs_test_capability", 202}, {"paid", slug, "cs_test_capability", 200}, {"refunded", slug, "cs_test_capability", 409}, {"paid", "wrong-event", "cs_test_capability", 404}, {"paid", slug, "cs_unknown", 404}, {"paid", slug, "invalid", 400}} {
		mustExec(t, database, `UPDATE bookings SET stripe_session_id='cs_test_capability',payment_status=? WHERE id=?`, tc.state, id)
		req := httptest.NewRequest("GET", "/v1/book/"+tc.slug+"/checkout?session_id="+tc.session, nil)
		req.SetPathValue("slug", tc.slug)
		rec := httptest.NewRecorder()
		h.BookingCheckout(rec, req)
		if rec.Code != tc.status {
			t.Fatalf("%+v: %d %s", tc, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("checkout can be cached")
		}
		if tc.status == 200 && !strings.Contains(rec.Body.String(), "manage_url") {
			t.Fatal("paid checkout lost calendar/manage details")
		}
	}
}
func TestCancelledPaidCheckoutCannotConfirmAgain(t *testing.T) {
	h, database, key, owner := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	seedFullAvailabilityDB(t, database, owner)
	id := createBookingViaHTTP(t, h, slug, futureAt(4, 10, 0).Format(time.RFC3339))
	mustExec(t, database, `UPDATE bookings SET stripe_session_id='cs_cancelled',payment_status='paid',status='cancelled' WHERE id=?`, id)
	req := httptest.NewRequest("GET", "/v1/book/"+slug+"/checkout?session_id=cs_cancelled", nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.BookingCheckout(rec, req)
	if rec.Code != 409 {
		t.Fatalf("cancelled checkout reappeared: %d %s", rec.Code, rec.Body.String())
	}
}

func TestTestEmailUsesDraftWithoutSaving(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	slug, id := seedEventTypeHTTP(t, h, key)
	stub := &stubMailer{}
	h.SetMailer(stub, "http://localhost")
	req := authReq(http.MethodPost, "/v1/event-types/"+slug+"/test-email", `{"type":"confirmation","draft":{"mode":"html","subject":"Draft subject","body":"<p>Draft body</p>"}}`, key)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.SendTestEmail)(rec, req)
	if rec.Code != 200 || stub.lastMsg.Subject != "[TEST] Draft subject" || !strings.Contains(stub.lastMsg.HTML, "Draft body") {
		t.Fatalf("stale draft: %d %+v", rec.Code, stub.lastMsg)
	}
	var saved string
	database.QueryRow(`SELECT COALESCE(html_confirmation,'') FROM event_types WHERE id=?`, id).Scan(&saved)
	if saved != "" {
		t.Fatal("test delivery saved the draft")
	}
	stub.lastMsg = mailer.Message{}
	req = authReq(http.MethodPost, "/v1/event-types/"+slug+"/test-email", `{"type":"confirmation","draft":{"mode":"invalid"}}`, key)
	req.SetPathValue("slug", slug)
	rec = httptest.NewRecorder()
	h.RequireAuth(h.SendTestEmail)(rec, req)
	if rec.Code != 400 || len(stub.lastMsg.To) != 0 {
		t.Fatal("invalid draft was sent")
	}
}
