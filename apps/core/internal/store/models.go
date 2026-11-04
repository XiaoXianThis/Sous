package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Model providers and which model each role uses, configured on the page
// (internal/models). Like settings they are local configuration, not
// domain events, so they bypass the event log; API keys live in their own
// table and are never read back out through the API.

type ModelProvider struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	BaseURL   string   `json:"base_url"`
	APIKeyEnv string   `json:"api_key_env"` // read the key from this Hub environment variable instead
	Models    []string `json:"models"`
	CreatedAt int64    `json:"created_at"`
	UpdatedAt int64    `json:"updated_at"`
}

// ModelRole is the model one role (supervisor, reviewer, ...) uses.
type ModelRole struct {
	ProviderID string `json:"provider_id"`
	Model      string `json:"model"`
	Effort     string `json:"effort,omitempty"`
}

const modelRolesKey = "models.roles"

func (s *Store) ModelProviders(ctx context.Context) ([]ModelProvider, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, kind, base_url, api_key_env, models, created_at, updated_at FROM model_providers ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ModelProvider{}
	for rows.Next() {
		var p ModelProvider
		var models string
		if err := rows.Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &p.APIKeyEnv, &models, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(models), &p.Models)
		if p.Models == nil {
			p.Models = []string{}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) ModelProvider(ctx context.Context, id string) (ModelProvider, error) {
	ps, err := s.ModelProviders(ctx)
	if err != nil {
		return ModelProvider{}, err
	}
	for _, p := range ps {
		if p.ID == id {
			return p, nil
		}
	}
	return ModelProvider{}, ErrNotFound
}

// PutModelProvider inserts or replaces a provider (not its key).
func (s *Store) PutModelProvider(ctx context.Context, p ModelProvider) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.Models == nil {
		p.Models = []string{}
	}
	models, _ := json.Marshal(p.Models)
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `INSERT INTO model_providers(id, name, kind, base_url, api_key_env, models, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name = excluded.name, kind = excluded.kind, base_url = excluded.base_url,
		api_key_env = excluded.api_key_env, models = excluded.models, updated_at = excluded.updated_at`,
		p.ID, p.Name, p.Kind, p.BaseURL, p.APIKeyEnv, string(models), now, now)
	return err
}

// DeleteModelProvider removes a provider and its key.
func (s *Store) DeleteModelProvider(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM model_keys WHERE provider_id = ?`, id); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM model_providers WHERE id = ?`, id)
	return err
}

// ModelKey returns a provider's stored API key, or "".
func (s *Store) ModelKey(ctx context.Context, id string) (string, error) {
	var k string
	err := s.db.QueryRowContext(ctx, `SELECT api_key FROM model_keys WHERE provider_id = ?`, id).Scan(&k)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return k, err
}

// SetModelKey stores a provider's API key; empty removes it.
func (s *Store) SetModelKey(ctx context.Context, id, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var err error
	if key == "" {
		_, err = s.db.ExecContext(ctx, `DELETE FROM model_keys WHERE provider_id = ?`, id)
	} else {
		_, err = s.db.ExecContext(ctx, `INSERT INTO model_keys(provider_id, api_key) VALUES (?,?)
			ON CONFLICT(provider_id) DO UPDATE SET api_key = excluded.api_key`, id, key)
	}
	if err == nil {
		_, err = s.db.ExecContext(ctx, `UPDATE model_providers SET updated_at = ? WHERE id = ?`, time.Now().UnixMilli(), id)
	}
	return err
}

// ModelRoles returns the configured roles (only the ones that are set).
func (s *Store) ModelRoles(ctx context.Context) (map[string]ModelRole, error) {
	out := map[string]ModelRole{}
	raw, err := s.Setting(ctx, modelRolesKey)
	if errors.Is(err, ErrNotFound) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(raw), &out)
	return out, nil
}

func (s *Store) SetModelRoles(ctx context.Context, roles map[string]ModelRole) error {
	raw, _ := json.Marshal(roles)
	return s.SetSetting(ctx, modelRolesKey, string(raw))
}
