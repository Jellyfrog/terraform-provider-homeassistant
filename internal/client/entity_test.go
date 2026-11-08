package client

import (
	"context"
	"maps"
	"slices"
	"testing"
)

func TestEntityRegistry(t *testing.T) {
	ctx := context.Background()
	entry := map[string]any{
		"entity_id": "sensor.moon_phase", "platform": "moon", "name": nil, "labels": []string{},
		"hidden_by": "integration",
	}
	notFound := &WSError{Code: "not_found", Message: "Entity not found"}
	var updateParams map[string]any
	entities := Entities(fakeWS{
		"config/entity_registry/get": func(params map[string]any) (any, error) {
			if params["entity_id"] != entry["entity_id"] {
				return nil, notFound
			}
			return entry, nil
		},
		"config/entity_registry/update": func(params map[string]any) (any, error) {
			if params["entity_id"] != entry["entity_id"] {
				return nil, notFound
			}
			updateParams = params
			maps.Copy(entry, params)
			return map[string]any{"entity_entry": entry}, nil
		},
	})

	got, ok, err := entities.Get(ctx, "sensor.moon_phase")
	if err != nil || !ok || got.Platform != "moon" || got.Name != nil || *got.HiddenBy != "integration" {
		t.Errorf("Get = %+v, %v, %v", got, ok, err)
	}
	if _, ok, err := entities.Get(ctx, "sensor.missing"); ok || err != nil {
		t.Errorf("Get(missing) = %v, %v; want not found and no error", ok, err)
	}

	updated, err := entities.Update(ctx, "sensor.moon_phase", map[string]any{"name": "Moon"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name == nil || *updated.Name != "Moon" {
		t.Errorf("updated = %+v", updated)
	}
	if updateParams["entity_id"] != "sensor.moon_phase" {
		t.Errorf("update params = %v", updateParams)
	}
}

func TestEntityRegistryFind(t *testing.T) {
	ctx := context.Background()
	notFound := &WSError{Code: "not_found", Message: "Entity not found"}
	registry := []map[string]any{
		{"id": "abc", "entity_id": "sensor.renamed", "platform": "moon"},
		{"id": "def", "entity_id": "sensor.moon_phase", "platform": "moon"},
	}
	var calls []string
	entities := Entities(fakeWS{
		"config/entity_registry/get": func(params map[string]any) (any, error) {
			calls = append(calls, "get")
			for _, e := range registry {
				if e["entity_id"] == params["entity_id"] {
					return e, nil
				}
			}
			return nil, notFound
		},
		"config/entity_registry/list": func(map[string]any) (any, error) {
			calls = append(calls, "list")
			return registry, nil
		},
	})

	tests := []struct {
		name, id, entityID string
		wantOK             bool
		wantEntityID       string
		wantCalls          []string
	}{
		{"get finds it", "abc", "sensor.renamed", true, "sensor.renamed", []string{"get"}},
		{"renamed: get misses", "abc", "sensor.old", true, "sensor.renamed", []string{"get", "list"}},
		{"renamed: get finds another entry", "abc", "sensor.moon_phase", true, "sensor.renamed", []string{"get", "list"}},
		{"gone", "xyz", "sensor.old", false, "", []string{"get", "list"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls = nil
			got, ok, err := entities.Find(ctx, tt.id, tt.entityID)
			if err != nil || ok != tt.wantOK || got.EntityID != tt.wantEntityID {
				t.Errorf("Find = %+v, %v, %v; want entity %q, ok %v", got, ok, err, tt.wantEntityID, tt.wantOK)
			}
			if !slices.Equal(calls, tt.wantCalls) {
				t.Errorf("calls = %q, want %q", calls, tt.wantCalls)
			}
		})
	}
}
