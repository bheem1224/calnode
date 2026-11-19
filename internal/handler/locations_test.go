package handler

import (
	"github.com/calnode/calnode/internal/i18n"
	"testing"
)

func TestEventLocationChoicesIncludePrimaryAndDeduplicate(t *testing.T) {
	options := []LocationOption{{Type: "teams", Value: "https://teams.microsoft.com/l/meetup-join/test"}, {Type: "google_meet", Value: "https://meet.google.com/abc-defg-hij"}, {Type: "teams", Value: "https://teams.microsoft.com/l/meetup-join/test"}}
	choices := publicLocationOptions("google_meet", "https://meet.google.com/abc-defg-hij", options, i18n.Get("en"))
	if len(choices) != 2 || choices[0].Label != "Google Meet" || choices[1].Label != "Microsoft Teams" {
		t.Fatalf("choices: %+v", choices)
	}
	for _, selected := range []locationSelection{{Type: "google_meet", Value: choices[0].Value}, {Type: "teams", Value: choices[1].Value}, {Type: "teams"}} {
		kind, value, err := selectEventLocation("google_meet", choices[0].Value, options, &selected)
		if err != nil || kind != selected.Type || value == "" {
			t.Fatalf("selection %+v: %s %s %v", selected, kind, value, err)
		}
	}
	for _, selected := range []locationSelection{{Type: "zoom"}, {Type: "teams", Value: "https://attacker.example/"}, {Type: "google_meet", Value: "javascript:alert(1)"}} {
		if _, _, err := selectEventLocation("google_meet", choices[0].Value, options, &selected); err == nil {
			t.Fatalf("accepted forged selection %+v", selected)
		}
	}
	if _, _, err := selectEventLocation("google_meet", choices[0].Value, nil, &locationSelection{Type: "teams"}); err == nil {
		t.Fatal("unconfigured location ignored on single-location event")
	}
	if _, _, err := selectEventLocation("in_person", "Office", []LocationOption{{Type: "link", Value: "javascript:alert(1)"}}, &locationSelection{Type: "link"}); err == nil {
		t.Fatal("unsafe legacy URL accepted")
	}
	if _, _, err := selectEventLocation("link", "https://one.example", []LocationOption{{Type: "link", Value: "https://two.example"}}, &locationSelection{Type: "link"}); err == nil {
		t.Fatal("ambiguous choice accepted")
	}
	for _, index := range []int{-1, 99} {
		if _, _, err := selectEventLocation("google_meet", choices[0].Value, options, &locationSelection{ID: &index}); err == nil {
			t.Fatal("out-of-range location ID accepted")
		}
	}
	index := 1
	if _, _, err := selectEventLocation("google_meet", choices[0].Value, options, &locationSelection{ID: &index, Type: "google_meet"}); err == nil {
		t.Fatal("location ID/type mismatch accepted")
	}
}
