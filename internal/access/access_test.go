package access_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/calnode/calnode/internal/access"
	"github.com/calnode/calnode/internal/db"
)

func TestCatalogAndLegacyScopes(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range access.Catalog {
		if seen[p.ID] {
			t.Fatal("duplicate permission", p.ID)
		}
		seen[p.ID] = true
		for _, scope := range p.Scopes {
			if err := access.Validate(access.Grants{p.ID: scope}, access.Full(), false); err != nil {
				t.Fatal(err)
			}
			if p.Group == "event_types" && scope == "team" {
				t.Fatal("personal events advertise a team scope")
			}
			if p.Group == "team_event_types" && scope == "own" {
				t.Fatal("team events advertise an own scope")
			}
		}
	}
	if access.Validate(access.Grants{"event_types.update": "team"}, access.Full(), true) == nil {
		t.Fatal("new ambiguous grant accepted")
	}
	for _, action := range []string{"read", "create", "update", "delete", "links", "custom_times"} {
		g := access.Parse(`{"event_types.` + action + `":"team"}`)
		if g["event_types."+action] != "own" || g.Has("team_event_types."+action) {
			t.Fatal("legacy grant widened", g)
		}
	}
	for _, raw := range []string{"null", "[]", `{"x":3}`, "invalid"} {
		if len(access.Parse(raw)) != 0 {
			t.Fatalf("invalid grants accepted: %s", raw)
		}
	}
}

func TestProfilesUseOneCatalogAndRespectCeilings(t *testing.T) {
	full := access.Full()
	presets := access.Presets(full)
	for _, name := range []string{"read_only", "scheduling", "event_manager", "workspace_operator"} {
		for id := range access.ProfileActions(name) {
			if !full.Has(id) || !presets[name].Has(id) {
				t.Fatalf("profile %s has unregistered or missing action %s", name, id)
			}
		}
	}
	if !reflect.DeepEqual(access.ProfileActions("assistant"), access.ProfileActions("scheduling")) {
		t.Fatal("scheduling presets drifted")
	}
	for _, id := range []string{"event_types.links", "event_types.custom_times", "team_event_types.links", "team_event_types.custom_times", "dynamic_links.create"} {
		if !presets["assistant"].Has(id) {
			t.Fatal("scheduling capability missing", id)
		}
	}
	for _, id := range []string{"event_types.update", "team_event_types.update", "settings.update"} {
		if presets["assistant"].Has(id) {
			t.Fatal("scheduling assistant can configure", id)
		}
	}
	if !presets["workspace_operator"].Has("logs.read") || presets["workspace_operator"].Has("transcripts.read") {
		t.Fatal("operator profile lost logs or gained private transcripts")
	}
	ceiling := access.Grants{"event_types.read": "own", "team_event_types.links": "team", "mcp.connect": "own"}
	for name, g := range access.Presets(ceiling) {
		if err := access.Validate(g, ceiling, false); err != nil {
			t.Fatalf("%s exceeds ceiling: %v", name, err)
		}
	}
}

func TestExplicitCustomRoleDoesNotInheritHeadGrants(t *testing.T) {
	d, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err = db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO users(id,email,name)VALUES('head','head@example.test','Head')`,
		`INSERT INTO teams(id,name,slug)VALUES('team','Team','team')`,
		`INSERT INTO team_members(team_id,user_id,role)VALUES('team','head','owner')`,
		`INSERT INTO roles(id,name,permissions_json)VALUES('custom','Custom','{"event_types.update":"own"}')`,
	} {
		if _, err = d.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	g, err := access.Load(ctx, d, "head")
	if err != nil || !g.Has("team_event_types.update") {
		t.Fatal("built-in Head lost authority", g, err)
	}
	if _, err = d.Exec(`UPDATE users SET custom_role_id='custom' WHERE id='head'`); err != nil {
		t.Fatal(err)
	}
	g, err = access.Load(ctx, d, "head")
	if err != nil || g.Has("team_event_types.update") {
		t.Fatal("explicit team denial overwritten", g, err)
	}
	if _, err = d.Exec(`UPDATE roles SET permissions_json='{"team_event_types.update":"team"}' WHERE id='custom'`); err != nil {
		t.Fatal(err)
	}
	g, err = access.Load(ctx, d, "head")
	if err != nil || !g.Has("team_event_types.update") {
		t.Fatal("explicit team grant lost", g, err)
	}
	if _, err = d.Exec(`INSERT INTO event_types(id,slug,name,user_id,team_id,duration_minutes)VALUES('event','event','Event','head','team',30)`); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"read", "update", "links", "custom_times"} {
		if access.EventActionAllowed(ctx, d, "head", access.Grants{"team_event_types." + action: "own"}, action, "event") {
			t.Fatal("unsupported team scope accepted", action)
		}
	}
}
