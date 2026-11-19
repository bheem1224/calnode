package handler

import (
	"fmt"
	"strings"

	"github.com/calnode/calnode/internal/i18n"
)

// The editor stores additional locations, not a replacement for the primary.
// Use one ordered, deduplicated list for rendering and booking validation.
func eventLocations(kind, value string, additional []LocationOption) []LocationOption {
	out := make([]LocationOption, 0, len(additional)+1)
	seen := map[LocationOption]bool{}
	for _, option := range append([]LocationOption{{Type: kind, Value: value}}, additional...) {
		option.Type = strings.TrimSpace(option.Type)
		option.Value = strings.TrimSpace(option.Value)
		if option.Type == "" || seen[option] {
			continue
		}
		seen[option] = true
		out = append(out, option)
	}
	return out
}

func publicLocationOptions(kind, value string, additional []LocationOption, loc *i18n.Locale) []bookLocationOption {
	out := []bookLocationOption{}
	for index, option := range eventLocations(kind, value, additional) {
		out = append(out, bookLocationOption{ID: index, Type: option.Type, Value: option.Value, Label: locationLabel(option.Type, option.Value, loc)})
	}
	return out
}

type locationSelection struct {
	Type  string `json:"type"`
	Value string `json:"value"`
	ID    *int   `json:"id,omitempty"`
}

func selectEventLocation(kind, value string, additional []LocationOption, selected *locationSelection) (string, string, error) {
	if selected == nil {
		return kind, value, nil
	} // Existing API clients retain the configured default.
	selectedType := strings.TrimSpace(selected.Type)
	selectedValue := strings.TrimSpace(selected.Value)
	var matches []LocationOption
	options := eventLocations(kind, value, additional)
	if selected.ID != nil {
		index := *selected.ID
		if index < 0 || index >= len(options) {
			return "", "", fmt.Errorf("selected_location is not configured for this event type")
		}
		option := options[index]
		if (selectedType != "" && selectedType != option.Type) || (selectedValue != "" && selectedValue != option.Value) {
			return "", "", fmt.Errorf("selected_location does not match the configured choice")
		}
		matches = append(matches, option)
	} else {
		for _, option := range options {
			if option.Type == selectedType && (option.Value == selectedValue || selectedValue == "") {
				matches = append(matches, option)
			}
		}
	}
	if len(matches) != 1 {
		return "", "", fmt.Errorf("selected_location is not configured or is ambiguous for this event type")
	}
	// Reject unsafe legacy option values too, without depending on the editor.
	option := matches[0]
	switch option.Type {
	case "link", "custom_video":
		if !validVideoURL(option.Type, option.Value) {
			return "", "", fmt.Errorf("configured location URL is invalid")
		}
	case "zoom":
		if option.Value != "" && !validVideoURL(option.Type, option.Value) {
			return "", "", fmt.Errorf("configured location URL is invalid")
		}
	case "google_meet", "teams":
		if option.Value != "" && !validMeetingLink(option.Type, option.Value) {
			return "", "", fmt.Errorf("configured location URL is invalid")
		}
	}
	// Never use a submitted URL/number: the configured value is authoritative.
	return matches[0].Type, matches[0].Value, nil
}
