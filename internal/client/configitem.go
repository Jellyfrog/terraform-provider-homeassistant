package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// ConfigItems manages the items of one REST config editor, `/api/config/<domain>/config/<id>`:
// automations, scripts, or scenes. Each item has a user-chosen ID (ADR-0008) and a config object
// (ADR-0005).
type ConfigItems struct {
	rest   *RESTClient
	domain string
	// idInBody is true for domains that store the ID inside the item (automation, scene), as
	// opposed to as its key (script).
	idInBody bool
}

// NewAutomations returns the config editor of automations.
func NewAutomations(rest *RESTClient) ConfigItems {
	return ConfigItems{rest: rest, domain: "automation", idInBody: true}
}

// NewScenes returns the config editor of scenes.
func NewScenes(rest *RESTClient) ConfigItems {
	return ConfigItems{rest: rest, domain: "scene", idInBody: true}
}

// NewScripts returns the config editor of scripts.
func NewScripts(rest *RESTClient) ConfigItems {
	return ConfigItems{rest: rest, domain: "script"}
}

func (c ConfigItems) path(id string) string {
	return "config/" + c.domain + "/config/" + id
}

// Get returns the config of the item as JSON, without its `id` key. A missing item is
// ErrNotFound.
func (c ConfigItems) Get(ctx context.Context, id string) (json.RawMessage, error) {
	var config map[string]json.RawMessage
	if err := c.rest.Get(ctx, c.path(id), &config); err != nil {
		return nil, err
	}
	if config == nil {
		return nil, fmt.Errorf("%s %q: config is not an object", c.domain, id)
	}
	delete(config, "id")
	return json.Marshal(config)
}

// Save creates or replaces the item. config is a JSON object without an `id` key. HA validates
// it, and a rejected config is an *HTTPError with status 400 and HA's message. HA reloads the
// domain asynchronously afterwards.
func (c ConfigItems) Save(ctx context.Context, id string, config json.RawMessage) error {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(config, &body); err != nil || body == nil {
		return fmt.Errorf("%s %q: config must be a JSON object", c.domain, id)
	}
	if c.idInBody {
		body["id"], _ = json.Marshal(id)
	}
	return c.rest.Do(ctx, http.MethodPost, c.path(id), body, nil)
}

// Delete deletes the item. A missing item is ErrNotFound.
func (c ConfigItems) Delete(ctx context.Context, id string) error {
	return c.rest.Do(ctx, http.MethodDelete, c.path(id), nil, nil)
}

// ErrEntityNotFound means the item has no entity (yet): HA reloads asynchronously after Save.
var ErrEntityNotFound = errors.New("entity not found")

// FixedEntityID returns the entity ID of the item for a domain that stores the ID as the item's
// key (script): `<domain>.<id>`. It returns false for a domain whose entity ID derives from the
// item's name (automation, scene).
func (c ConfigItems) FixedEntityID(id string) (string, bool) {
	if c.idInBody {
		return "", false
	}
	return c.domain + "." + id, true
}

// FindEntity looks up the entity ID of the item once; it does not wait for a reload. For a
// domain with a fixed entity ID, it reads that entity's state. Otherwise the entity is the one of
// the domain whose `attributes.id` is id: known, the entity ID found before, is checked first with
// `GET /api/states/<known>`, and only if it doesn't match (or is empty) are all states read. No
// such entity is ErrEntityNotFound.
func (c ConfigItems) FindEntity(ctx context.Context, id, known string) (string, error) {
	if entityID, ok := c.FixedEntityID(id); ok {
		_, err := c.rest.State(ctx, entityID)
		if errors.Is(err, ErrNotFound) {
			return "", fmt.Errorf("%w: no entity %s", ErrEntityNotFound, entityID)
		}
		return entityID, err
	}

	if known != "" && c.inDomain(known) {
		s, err := c.rest.State(ctx, known)
		switch {
		case errors.Is(err, ErrNotFound):
		case err != nil:
			return "", err
		default:
			var attrs struct {
				ID any `json:"id"`
			}
			if json.Unmarshal(s.Attributes, &attrs) == nil && attrs.ID == id {
				return known, nil
			}
		}
	}

	var states []struct {
		EntityID   string `json:"entity_id"`
		Attributes struct {
			ID any `json:"id"`
		} `json:"attributes"`
	}
	if err := c.rest.Get(ctx, "states", &states); err != nil {
		return "", err
	}
	for _, s := range states {
		if c.inDomain(s.EntityID) && s.Attributes.ID == id {
			return s.EntityID, nil
		}
	}
	return "", fmt.Errorf("%w: no %s entity has attributes.id %q", ErrEntityNotFound, c.domain, id)
}

// inDomain reports whether entityID is in the item's domain.
func (c ConfigItems) inDomain(entityID string) bool {
	return EntityDomain(entityID) == c.domain
}
