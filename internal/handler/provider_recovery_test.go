package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestProviderRecoveryHealthAndRetryNeverResetUncertainCreate(t *testing.T) {
	h, _ := alertSeed(t)
	securityExec(t, h, `INSERT INTO zoom_meeting_intents(operation_key,user_id,marker,topic,state,next_attempt_at,last_error)VALUES('booking:unknown','owner','marker','Meeting','uncertain','2099-01-01T00:00:00Z','pending');INSERT INTO calendar_event_cleanup(id,booking_id,user_id,provider,calendar_ref,event_id,next_attempt_at,last_error)VALUES('retired-seat','booking','owner','google','private-calendar','old-event','2099-01-01T00:00:00Z','pending')`)
	cookie := securitySession(t, h, "owner", true, false)
	rec := securityCall(h, h.GetSecurityOperations, cookie, "GET", "/v1/settings/security/operations", "", nil)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
	var health struct {
		Work []struct {
			ID string `json:"id"`
		} `json:"provider_work"`
		Pending int `json:"pending_jobs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &health); err != nil || len(health.Work) != 2 || health.Pending < 2 {
		t.Fatal("pending work hidden", rec.Body, err)
	}
	for _, id := range []string{"zoom-recovery:booking:unknown", "calendar-cleanup:retired-seat"} {
		rec = securityCall(h, h.RetrySecurityJob, cookie, "POST", "/v1/settings/security/jobs/"+id+"/retry", "{}", map[string]string{"id": id})
		if rec.Code != 200 {
			t.Fatal("recovery retry", rec.Code, rec.Body)
		}
	}
	var state, next string
	if err := h.db.QueryRow(`SELECT state,next_attempt_at FROM zoom_meeting_intents WHERE operation_key='booking:unknown'`).Scan(&state, &next); err != nil || state != "uncertain" || next != "" {
		t.Fatal("retry authorized another POST", state, next, err)
	}
	securityExec(t, h, `UPDATE sessions SET reauthenticated_at=NULL,mfa_verified_at=NULL WHERE user_id='owner'`)
	rec = securityCall(h, h.RetrySecurityJob, cookie, "POST", "/v1/settings/security/jobs/zoom-recovery:booking:unknown/retry", "{}", map[string]string{"id": "zoom-recovery:booking:unknown"})
	if rec.Code != 403 {
		t.Fatal("recovery retry bypassed identity verification", rec.Code)
	}
}

func TestProviderRecoveryStaleSuccessCannotClearNewerTimeDrift(t *testing.T) {
	var logs bytes.Buffer
	h, database, _ := newReconcileFixture(t, "confirmed", &logs)
	var start, end string
	if err := database.QueryRow(`SELECT start_at,end_at FROM bookings WHERE id='bk-1'`).Scan(&start, &end); err != nil {
		t.Fatal(err)
	}
	a, _ := time.Parse(time.RFC3339, start)
	b, _ := time.Parse(time.RFC3339, end)
	newStart, newEnd := a.Add(time.Hour), b.Add(time.Hour)
	if _, err := database.Exec(`UPDATE bookings SET start_at=?,end_at=? WHERE id='bk-1'`, newStart.Format(time.RFC3339), newEnd.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	h.clearCalendarSync(context.Background(), "bk-1", "host-1", a, b)
	_, dirty := hostRow(t, database)
	if dirty != 1 {
		t.Fatal("older response discarded newer reschedule work")
	}
	h.clearCalendarSync(context.Background(), "bk-1", "host-1", newStart, newEnd)
	_, dirty = hostRow(t, database)
	if dirty != 0 {
		t.Fatal("current reschedule did not clear drift")
	}
}
