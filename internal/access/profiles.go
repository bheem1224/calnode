package access

// AgentActions is the supported agent surface, expressed in the same catalog as
// users and API keys. Credential issuance, recovery and secrets remain interactive.
func AgentActions() []string {
	return []string{"event_types.read", "event_types.create", "event_types.update", "event_types.delete", "event_types.links", "event_types.custom_times", "team_event_types.read", "team_event_types.create", "team_event_types.update", "team_event_types.delete", "team_event_types.links", "team_event_types.custom_times", "dynamic_links.create", "availability.read", "availability.update", "bookings.read", "bookings.create", "bookings.reschedule", "bookings.cancel", "notes.read", "transcripts.read", "profile.read", "profile.update", "teams.read", "teams.create", "teams.update", "teams.delete", "settings.read", "settings.update", "branding.read", "branding.update", "integrations.read", "integrations.update", "logs.read"}
}

// ProfileActions supplies the operation bundles used by role/key editors and MCP
// consent. Profiles select grants once; they are not a separate authority system.
func ProfileActions(profile string) map[string]bool {
	out := map[string]bool{}
	add := func(ids ...string) {
		for _, id := range ids {
			out[id] = true
		}
	}
	switch profile {
	case "assistant", "coordinator":
		profile = "scheduling"
	case "reporting":
		profile = "read_only"
	}
	switch profile {
	case "read_only", "scheduling", "event_manager", "workspace_operator":
		add("event_types.read", "team_event_types.read", "availability.read", "bookings.read")
	case "invitations":
		add("members.invite_member")
	case "analyst":
		add("bookings.read", "notes.read", "transcripts.read")
	case "concierge":
		add("event_types.read", "team_event_types.read", "event_types.links", "event_types.custom_times", "team_event_types.links", "team_event_types.custom_times", "dynamic_links.create")
	default:
		return out
	}
	if profile == "scheduling" || profile == "event_manager" || profile == "workspace_operator" {
		add("bookings.create", "bookings.reschedule", "bookings.cancel", "event_types.links", "event_types.custom_times", "team_event_types.links", "team_event_types.custom_times", "dynamic_links.create")
	}
	if profile == "event_manager" || profile == "workspace_operator" {
		add("profile.read", "profile.update", "teams.read", "availability.update", "integrations.read", "integrations.update", "event_types.create", "event_types.update", "event_types.delete", "team_event_types.create", "team_event_types.update", "team_event_types.delete")
	}
	if profile == "workspace_operator" {
		for _, id := range AgentActions() {
			if id != "notes.read" && id != "transcripts.read" {
				add(id)
			}
		}
	}
	return out
}

// Presets is projected onto the caller before it leaves the server. The editor
// also filters non-delegable actions when these selections are used for a key.
func Presets(ceiling Grants) map[string]Grants {
	out := map[string]Grants{"admin": Intersect(Full(), ceiling), "member": Intersect(Member(false), ceiling)}
	for _, name := range []string{"read_only", "scheduling", "event_manager", "workspace_operator", "assistant", "reporting", "invitations", "analyst", "coordinator", "concierge"} {
		g := Grants{}
		for id := range ProfileActions(name) {
			if ceiling.Has(id) {
				g[id] = ceiling[id]
			}
		}
		// Role/key profiles can grant access to connect and manage the user's
		// own agents. Those transport permissions are not MCP tool operations.
		for _, id := range []string{"mcp.connect", "mcp.manage"} {
			if name != "invitations" && ceiling.Has(id) {
				g[id] = "own"
			}
		}
		out[name] = g
	}
	return out
}
