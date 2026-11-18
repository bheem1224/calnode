package webhook

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

func (s *Service) RotateSecret(ctx context.Context, userID, id string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	encrypted, err := s.encrypt(raw)
	if err != nil {
		return "", err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE webhooks SET secret_enc=? WHERE id=? AND user_id=?`, encrypted, id, userID)
	if err != nil {
		return "", err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return "", ErrNotFound
	}
	return hex.EncodeToString(raw), nil
}
