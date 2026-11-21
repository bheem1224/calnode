package handler

import (
	"context"
	"testing"
)

func TestTeamPrioritySharedByBookingHostsAndDraftPreview(t *testing.T) {
	h := rbacFixture(t)
	ctx := context.Background()
	for _, q := range []string{`INSERT INTO users(id,name,email)VALUES('first','First','first@example.test'),('second','Second','second@example.test')`, `INSERT INTO team_members(id,team_id,user_id,role,routing_priority)VALUES('p1','a','first','member',10),('p2','a','second','member',0)`, `INSERT INTO event_type_hosts(id,event_type_id,user_id,role,priority)VALUES('h1','team-a','first','rotation',0),('h2','team-a','second','rotation',1)`} {
		rbacExec(t, h, q)
	}
	hosts, err := h.resolveEventTypeHosts(ctx, "team-a")
	if err != nil || len(hosts) != 2 || hosts[0].UserID != "second" {
		t.Fatalf("team priority did not affect real hosts: %v %+v", err, hosts)
	}
	draft := []EventHost{{UserID: "first", Priority: 0}, {UserID: "second", Priority: 1}}
	if err = h.orderEventTypeHosts(ctx, "team-a", draft); err != nil || draft[0].UserID != "second" {
		t.Fatal("preview priority drift", err, draft)
	}
	rbacExec(t, h, `UPDATE team_members SET routing_priority=0 WHERE team_id='a'`)
	hosts, err = h.resolveEventTypeHosts(ctx, "team-a")
	if err != nil || hosts[0].UserID != "first" {
		t.Fatal("event order did not break team tie", err, hosts)
	}
	draft = []EventHost{{UserID: "first", Priority: 1}, {UserID: "second", Priority: 0}}
	if err = h.orderEventTypeHosts(ctx, "personal", draft); err != nil || draft[0].UserID != "second" {
		t.Fatal("personal event inherited team priorities")
	}
}
