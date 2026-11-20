package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
)

// The import adapter composes the existing create and patch handlers instead of
// maintaining a second event model/validator. It cannot publish an import.
func agentFields(spec agentOperationSpec) []string {
	if handlerName(spec.handler) != "importAgentEventType" {
		return agentRequestFields[handlerName(spec.handler)]
	}
	fields := append([]string{}, agentRequestFields["CreateEventType"]...)
	for _, key := range agentRequestFields["PatchEventType"] {
		found := false
		for _, existing := range fields {
			found = found || existing == key
		}
		if !found {
			fields = append(fields, key)
		}
	}
	return fields
}

func (h *Handler) importAgentEventType(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	var values map[string]any
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&values) != nil {
		h.writeError(w, 400, "event configuration is required")
		return
	}
	values["is_active"], values["is_public"] = false, false
	base := map[string]any{}
	for _, key := range agentRequestFields["CreateEventType"] {
		if value, present := values[key]; present {
			base[key] = value
		}
	}
	call := func(fn http.HandlerFunc, name, target string, input map[string]any) *httptest.ResponseRecorder {
		spec := h.agentOperations()[name]
		req, err := h.operationRequest(r.Context(), agentOperation{Name: name, Target: target, Values: input}, spec)
		result := httptest.NewRecorder()
		if err != nil {
			h.writeError(result, 400, err.Error())
		} else {
			h.authorizedCall(result, req, u, fn)
		}
		return result
	}
	created := call(h.CreateEventType, "create_event_type", "", base)
	if created.Code >= 400 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(created.Code)
		_, _ = w.Write(created.Body.Bytes())
		return
	}
	var event eventTypeJSON
	if json.Unmarshal(created.Body.Bytes(), &event) != nil || event.Slug == "" {
		h.writeError(w, 500, "import draft created but its identifier could not be read; inspect event types before retrying")
		return
	}
	patch := map[string]any{}
	for _, key := range agentRequestFields["PatchEventType"] {
		if value, present := values[key]; present {
			patch[key] = value
		}
	}
	updated := call(h.PatchEventType, "update_event_type", event.Slug, patch)
	if updated.Code >= 400 {
		var problem any
		_ = json.Unmarshal(updated.Body.Bytes(), &problem)
		h.writeJSON(w, 409, map[string]any{"error": "advanced configuration failed; an inactive/private draft remains, correct it instead of repeating the import", "slug": event.Slug, "validation": redactAgentData(problem)})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	_, _ = w.Write(updated.Body.Bytes())
}
