package mailer

import (
	"context"
	"errors"
	"testing"

	"github.com/calnode/calnode/internal/abuse"
	"github.com/calnode/calnode/internal/db"
)

type countedMailer struct{ sent int }

func (m *countedMailer) Send(context.Context, Message) error { m.sent++; return nil }

func TestLiveBudgetsSurviveTransportSwapAndNormalizeRecipients(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EMAIL_RECIPIENT_HOURLY_LIMIT", "1")
	first := &countedMailer{}
	live := NewLive(first)
	live.SetBudgetDB(database)
	if err = live.Send(context.Background(), Message{To: []string{"person@example.com"}}); err != nil {
		t.Fatal(err)
	}
	second := &countedMailer{}
	live.Swap(second)
	if err = live.Send(context.Background(), Message{To: []string{"PERSON@example.com"}}); !errors.Is(err, abuse.ErrLimited) {
		t.Fatal("swapping transport reset budget", err)
	}
	if first.sent != 1 || second.sent != 0 {
		t.Fatal("limited message reached transport")
	}
	if err = live.Send(context.Background(), Message{To: []string{"other@example.com"}}); err != nil {
		t.Fatal("unrelated recipient blocked", err)
	}
}
