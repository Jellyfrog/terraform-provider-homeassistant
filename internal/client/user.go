package client

import "context"

// The system groups of Home Assistant. A user is in exactly one of them.
const (
	GroupAdmin    = "system-admin"
	GroupUser     = "system-users"
	GroupReadOnly = "system-read-only"
)

// User is a Home Assistant user, as `config/auth/list` reports it.
type User struct {
	ID string `json:"id"`
	// Username is the login of the user's Home Assistant credentials (the `homeassistant` auth
	// provider), or nil if it has none.
	Username        *string  `json:"username"`
	Name            string   `json:"name"`
	IsOwner         bool     `json:"is_owner"`
	IsActive        bool     `json:"is_active"`
	LocalOnly       bool     `json:"local_only"`
	SystemGenerated bool     `json:"system_generated"`
	GroupIDs        []string `json:"group_ids"`
	// Credentials are the user's logins, one per auth provider credential.
	Credentials []Credential `json:"credentials"`
}

// Credential is one login of a user, by the type of its auth provider, e.g. `homeassistant`.
type Credential struct {
	Type string `json:"type"`
}

// Logins returns how many Home Assistant credentials (username and password) the user has.
// Username reports only the first; Home Assistant before 2026.11 allows more than one.
func (u User) Logins() int {
	n := 0
	for _, c := range u.Credentials {
		if c.Type == "homeassistant" {
			n++
		}
	}
	return n
}

// Users manages users (`config/auth/list|create|update|delete`, naming a user by `user_id`) and
// their Home Assistant credentials (`config/auth_provider/homeassistant/*`). Every command needs
// an admin token; changing an existing username or password also needs the owner's. Create takes
// `name`, `group_ids`, and `local_only`; `is_active` can only be set by an update.
type Users struct {
	Registry[User]
}

// NewUsers returns the users of the instance behind ws, including the owner and the
// system-generated ones.
func NewUsers(ws WSCommander) Users {
	return Users{Registry[User]{
		ws:     ws,
		prefix: "config/auth",
		key:    "user_id",
		reply:  "user",
		idOf:   func(u User) string { return u.ID },
	}}
}

// Configured reports whether u is connected to an instance.
func (u Users) Configured() bool {
	return u.ws != nil
}

// Current returns the user whose token the client uses (`auth/current_user`). Only its ID, Name,
// and IsOwner are set: this command names a credential's type differently, so Logins is 0.
func (u Users) Current(ctx context.Context) (User, error) {
	var user User
	err := u.ws.Command(ctx, "auth/current_user", nil, &user)
	return user, err
}

// CreateCredentials gives the user with the given ID a username and password. The user must
// have none yet: Home Assistant before 2026.11 would attach a second login.
func (u Users) CreateCredentials(ctx context.Context, id, username, password string) error {
	return u.ws.Command(ctx, "config/auth_provider/homeassistant/create", map[string]any{
		"user_id": id, "username": username, "password": password,
	}, nil)
}

// DeleteCredentials removes the login with the given username, so its user can no longer log in
// with a password.
func (u Users) DeleteCredentials(ctx context.Context, username string) error {
	return u.ws.Command(ctx, "config/auth_provider/homeassistant/delete", map[string]any{"username": username}, nil)
}

// ChangeUsername renames the login of the user with the given ID. Only the owner may.
func (u Users) ChangeUsername(ctx context.Context, id, username string) error {
	return u.ws.Command(ctx, "config/auth_provider/homeassistant/admin_change_username", map[string]any{
		"user_id": id, "username": username,
	}, nil)
}

// ChangePassword sets the password of the user with the given ID. Only the owner may.
func (u Users) ChangePassword(ctx context.Context, id, password string) error {
	return u.ws.Command(ctx, "config/auth_provider/homeassistant/admin_change_password", map[string]any{
		"user_id": id, "password": password,
	}, nil)
}
