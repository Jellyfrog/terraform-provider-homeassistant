package client

import (
	"context"
	"errors"
	"maps"
	"slices"
)

// EntityEntry is an entry of the entity registry.
type EntityEntry struct {
	// ID is the registry ID. Unlike the entity ID, a rename does not change it (ADR-0025).
	ID           string   `json:"id"`
	EntityID     string   `json:"entity_id"`
	Name         *string  `json:"name"`
	Icon         *string  `json:"icon"`
	AreaID       *string  `json:"area_id"`
	Labels       []string `json:"labels"`
	HiddenBy     *string  `json:"hidden_by"`
	DisabledBy   *string  `json:"disabled_by"`
	Platform     string   `json:"platform"`
	DeviceID     *string  `json:"device_id"`
	OriginalName *string  `json:"original_name"`
}

// EntityRegistry is the entity registry (`config/entity_registry/*`). Integrations create and
// remove its entries; the provider only reads and updates them (ADR-0010).
type EntityRegistry struct {
	ws WSCommander
}

// Entities returns the entity registry.
func Entities(ws WSCommander) EntityRegistry {
	return EntityRegistry{ws: ws}
}

// List returns every entry.
func (r EntityRegistry) List(ctx context.Context) ([]EntityEntry, error) {
	var entries []EntityEntry
	err := r.ws.Command(ctx, "config/entity_registry/list", nil, &entries)
	return entries, err
}

// Get returns the entry of entityID. ok is false if there is none.
func (r EntityRegistry) Get(ctx context.Context, entityID string) (entry EntityEntry, ok bool, err error) {
	err = r.ws.Command(ctx, "config/entity_registry/get", map[string]any{"entity_id": entityID}, &entry)
	var wsErr *WSError
	if errors.As(err, &wsErr) && wsErr.Code == "not_found" {
		return entry, false, nil
	}
	return entry, err == nil, err
}

// Find returns the entry with the registry ID id, whose entity ID was entityID when last seen.
// It tries `get` by entityID first and lists the whole registry only if that misses or finds
// another entry, which happens after a rename (ADR-0025). ok is false if no entry has id.
func (r EntityRegistry) Find(ctx context.Context, id, entityID string) (entry EntityEntry, ok bool, err error) {
	entry, ok, err = r.Get(ctx, entityID)
	if err != nil || (ok && entry.ID == id) {
		return entry, ok, err
	}
	entries, err := r.List(ctx)
	if err != nil {
		return EntityEntry{}, false, err
	}
	if i := slices.IndexFunc(entries, func(e EntityEntry) bool { return e.ID == id }); i >= 0 {
		return entries[i], true, nil
	}
	return EntityEntry{}, false, nil
}

// Update sets fields on the entry of entityID and returns the result. A `new_entity_id` field
// renames the entity.
func (r EntityRegistry) Update(ctx context.Context, entityID string, fields map[string]any) (EntityEntry, error) {
	params := make(map[string]any, len(fields)+1)
	maps.Copy(params, fields)
	params["entity_id"] = entityID
	var result struct {
		EntityEntry EntityEntry `json:"entity_entry"`
	}
	err := r.ws.Command(ctx, "config/entity_registry/update", params, &result)
	return result.EntityEntry, err
}
