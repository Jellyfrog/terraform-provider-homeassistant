package client

import (
	"context"
	"errors"
	"maps"
)

// Registry is one of HA's WebSocket registries with the commands `<prefix>/create`, `/update`,
// and `/delete`, which name an object by its ID in the field `key`, and a list command
// (`<prefix>/list` unless list says otherwise). Floors, areas, labels, input helpers, and
// Lovelace resources follow this shape.
type Registry[T any] struct {
	ws     WSCommander
	prefix string
	key    string
	idOf   func(T) string
	// list is the list command, if it is not `<prefix>/list`.
	list string
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
	var item T
	err := r.ws.Command(ctx, r.prefix+"/create", fields, &item)
	return item, err
}

// Update sets fields on the object with the given ID and returns the result.
func (r Registry[T]) Update(ctx context.Context, id string, fields map[string]any) (T, error) {
	params := make(map[string]any, len(fields)+1)
	maps.Copy(params, fields)
	params[r.key] = id
	var item T
	err := r.ws.Command(ctx, r.prefix+"/update", params, &item)
	return item, err
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
// only asking again tells a missing object apart from a real failure.
func IgnoreGone(ctx context.Context, err error, exists func(context.Context) (bool, error)) error {
	var wsErr *WSError
	if errors.As(err, &wsErr) {
		if ok, getErr := exists(ctx); getErr == nil && !ok {
			return nil
		}
	}
	return err
}
