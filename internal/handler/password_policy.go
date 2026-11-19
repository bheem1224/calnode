package handler

import (
	"context"
	"fmt"
	"github.com/calnode/calnode/internal/authsecurity"
	"strings"
)

func (h *Handler) validatePassword(ctx context.Context, password string, account ...string) string {
	p, err := h.signinPolicy(ctx)
	if err != nil {
		return "password policy unavailable; please try again"
	}
	length := authsecurity.PasswordLength(password)
	if length < p.PasswordMinLength {
		return fmt.Sprintf("password must be at least %d characters", p.PasswordMinLength)
	}
	if length > 128 {
		return "password must be 128 characters or fewer"
	}
	if u, ok := userFromContext(ctx); ok {
		account = append(account, u.Name, u.Email, strings.Split(u.Email, "@")[0])
	}
	if authsecurity.BlockedPassword(password, account...) {
		return "choose a less predictable password; this password is common or compromised"
	}
	return ""
}
