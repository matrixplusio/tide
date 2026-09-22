package pg

import (
	"context"

	"gorm.io/gorm"
)

// Settings stores one JSON document per section. Encryption and redaction of
// secret fields happen in internal/settings before values reach this layer.
type Settings struct {
	db *gorm.DB
}

// Get returns nil when the section does not exist.
func (s *Settings) Get(ctx context.Context, section string) ([]byte, error) {
	var rows []struct{ Value []byte }
	if err := s.db.WithContext(ctx).Raw(`SELECT value FROM settings WHERE section = $1`, section).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0].Value, nil
}

// GetForUpdate locks the row for the rest of the transaction.
func (s *Settings) GetForUpdate(ctx context.Context, section string) ([]byte, error) {
	var rows []struct{ Value []byte }
	if err := s.db.WithContext(ctx).Raw(`SELECT value FROM settings WHERE section = $1 FOR UPDATE`, section).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0].Value, nil
}

func (s *Settings) Put(ctx context.Context, section string, value []byte, updatedBy string) error {
	return s.db.WithContext(ctx).Exec(`INSERT INTO settings (section, value, updated_by, updated_at) VALUES ($1, $2::jsonb, $3, now())
		ON CONFLICT (section) DO UPDATE SET value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = now()`,
		section, string(value), updatedBy).Error
}

// PutIfAbsent inserts only when no row exists (first replica wins).
func (s *Settings) PutIfAbsent(ctx context.Context, section string, value []byte, updatedBy string) error {
	return s.db.WithContext(ctx).Exec(`INSERT INTO settings (section, value, updated_by) VALUES ($1, $2::jsonb, $3)
		ON CONFLICT (section) DO NOTHING`, section, string(value), updatedBy).Error
}
