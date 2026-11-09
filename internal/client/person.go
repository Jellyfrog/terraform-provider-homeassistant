package client

import "context"

// Person is a person: someone whose presence Home Assistant tracks, optionally linked to a user.
type Person struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	UserID         *string  `json:"user_id"`
	DeviceTrackers []string `json:"device_trackers"`
	Picture        *string  `json:"picture"`
}

// Persons manages the persons kept in storage (`person/list|create|update|delete`, naming a
// person by `person_id`). Persons defined in YAML are listed too, but cannot be changed.
type Persons struct {
	// reg creates and updates; its list command does not fit `person/list`.
	reg Registry[Person]
}

// NewPersons returns the persons of the instance behind ws.
func NewPersons(ws WSCommander) Persons {
	return Persons{reg: Registry[Person]{
		ws: ws, prefix: "person", key: "person_id", idOf: func(p Person) string { return p.ID },
	}}
}

// List returns the persons in storage and those defined in YAML.
func (p Persons) List(ctx context.Context) (storage, yaml []Person, err error) {
	var res struct {
		Storage []Person `json:"storage"`
		Config  []Person `json:"config"`
	}
	err = p.reg.ws.Command(ctx, "person/list", nil, &res)
	return res.Storage, res.Config, err
}

// Get returns the person in storage with the given ID. ok is false if there is none; yaml is
// true if a person with that ID is defined in YAML instead.
func (p Persons) Get(ctx context.Context, id string) (person Person, ok, yaml bool, err error) {
	storage, yamlPersons, err := p.List(ctx)
	if err != nil {
		return person, false, false, err
	}
	for _, it := range storage {
		if it.ID == id {
			return it, true, false, nil
		}
	}
	for _, it := range yamlPersons {
		if it.ID == id {
			return person, false, true, nil
		}
	}
	return person, false, false, nil
}

// Create creates a person from fields (`name`, `user_id`, `device_trackers`, `picture`) and
// returns it with the ID Home Assistant derived from the name.
func (p Persons) Create(ctx context.Context, fields map[string]any) (Person, error) {
	return p.reg.Create(ctx, fields)
}

// Update sets fields on the person with the given ID. An omitted `device_trackers` clears the
// trackers, while an omitted `user_id` or `picture` is kept, so callers send every field.
func (p Persons) Update(ctx context.Context, id string, fields map[string]any) (Person, error) {
	return p.reg.Update(ctx, id, fields)
}

// Delete deletes the person with the given ID. A person that is already gone is not an error;
// one that is now defined in YAML is not gone.
func (p Persons) Delete(ctx context.Context, id string) error {
	err := p.reg.ws.Command(ctx, "person/delete", map[string]any{"person_id": id}, nil)
	return IgnoreGone(ctx, err, func(ctx context.Context) (bool, error) {
		_, ok, yaml, err := p.Get(ctx, id)
		return ok || yaml, err
	})
}
