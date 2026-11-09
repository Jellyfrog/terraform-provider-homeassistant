package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
)

// Registry is one of HA's WebSocket registries with the commands `<prefix>/create`, `/update`,
// and `/delete`, which name an object by its ID in the field `key`, and a list command
// (`<prefix>/list` unless list says otherwise). Floors, areas, labels, input helpers, Lovelace
// resources, and users follow this shape.
type Registry[T any] struct {
	ws     WSCommander
	prefix string
	key    string
	idOf   func(T) string
	// list is the list command, if it is not `<prefix>/list`.
	list string
	// reply is the key that wraps the object in create and update replies, e.g. `user` for
	// `{"user": {...}}`, or empty if the reply is the object itself.
	reply string
}

// command runs a create or update command and decodes the object from its reply.
func (r Registry[T]) command(ctx context.Context, typ string, params map[string]any) (T, error) {
	var item T
	if r.reply == "" {
		err := r.ws.Command(ctx, typ, params, &item)
		return item, err
	}
	// Only the wrapped object is decoded, so other keys in the reply do not break it.
	var wrapped map[string]json.RawMessage
	if err := r.ws.Command(ctx, typ, params, &wrapped); err != nil {
		return item, err
	}
	raw, ok := wrapped[r.reply]
	if !ok {
		return item, fmt.Errorf("%s: the reply has no %q", typ, r.reply)
	}
	err := json.Unmarshal(raw, &item)
	return item, err
}

// List returns every object in the registry.
func (r Registry[T]) List(ctx context.Context) ([]T, error) {
	cmd := r.list
	if cmd == "" {
		cmd = r.prefix + "/list"
	}
	var items []T
	err := r.ws.Command(ctx, cmd, nil, &items)
	return items, err
}

// Get returns the object with the given ID. ok is false if there is none.
func (r Registry[T]) Get(ctx context.Context, id string) (item T, ok bool, err error) {
	return r.Find(ctx, func(it T) bool { return r.idOf(it) == id })
}

// Find returns the first object for which match is true. ok is false if there is none.
func (r Registry[T]) Find(ctx context.Context, match func(T) bool) (item T, ok bool, err error) {
	items, err := r.List(ctx)
	if err != nil {
		return item, false, err
	}
	for _, it := range items {
		if match(it) {
			return it, true, nil
		}
	}
	return item, false, nil
}

// Create creates an object from fields and returns it as HA stored it, including the ID HA
// assigned.
func (r Registry[T]) Create(ctx context.Context, fields map[string]any) (T, error) {
	return r.command(ctx, r.prefix+"/create", fields)
}

// Update sets fields on the object with the given ID and returns the result.
func (r Registry[T]) Update(ctx context.Context, id string, fields map[string]any) (T, error) {
	params := make(map[string]any, len(fields)+1)
	maps.Copy(params, fields)
	params[r.key] = id
	return r.command(ctx, r.prefix+"/update", params)
}

// Delete deletes the object with the given ID. An object that is already gone is not an error.
func (r Registry[T]) Delete(ctx context.Context, id string) error {
	err := r.ws.Command(ctx, r.prefix+"/delete", map[string]any{r.key: id}, nil)
	return IgnoreGone(ctx, err, func(ctx context.Context) (bool, error) {
		_, ok, err := r.Get(ctx, id)
		return ok, err
	})
}

// IgnoreGone returns nil if err is an error from HA and exists then reports that the object the
// command was about is gone, and err otherwise. HA reports a missing ID as a generic error, so
// only asking again tells a missing object apart from a real failure. An unknown command is never
// a missing object: the command does not exist on this instance (e.g. Lovelace resources in YAML
// mode).
func IgnoreGone(ctx context.Context, err error, exists func(context.Context) (bool, error)) error {
	var wsErr *WSError
	if errors.As(err, &wsErr) && wsErr.Code != "unknown_command" {
		if ok, getErr := exists(ctx); getErr == nil && !ok {
			return nil
		}
	}
	return err
}
