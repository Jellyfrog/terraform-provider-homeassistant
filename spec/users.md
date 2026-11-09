# Users and persons

Both use the WebSocket transport. A **user** can log in; a **person** is someone whose presence
Home Assistant tracks, and is optionally linked to a user. The UI creates both together. The
provider manages them as two resources, linked by `homeassistant_person.user_id`.

## `homeassistant_user`

Commands: `config/auth/list|create|update|delete` (key `user_id`), and for the login
`config/auth_provider/homeassistant/create|delete|admin_change_username|admin_change_password`.

| Argument | Type | |
|---|---|---|
| `id` | string, computed | Random hex ID that HA assigns |
| `name` | string, required | Display name |
| `username` | string, optional | Login name. Lowercase, no spaces at the start or end (HA rejects others) |
| `password` | string, optional, sensitive | Kept in state, like the secrets of `homeassistant_mqtt`. Works from 1.8 (ADR-0016). Needs `username`; conflicts with `password_wo` |
| `password_wo` | string, optional, sensitive, write-only | Never in plan or state. Needs OpenTofu/Terraform ≥ 1.11; older clients get the framework's "WriteOnly Attribute Not Allowed" error when it is set. Needs `username` |
| `password_wo_version` | number, optional | Changing it sets the password from `password_wo`. Needs `password_wo` |
| `group` | string, default `user` | `admin`, `user` or `read-only`: the system groups `system-admin`, `system-users`, `system-read-only` |
| `is_active` | bool, default `true` | Whether the user can log in |
| `local_only` | bool, default `false` | Whether the user can log in only from the local network |

- **Create:** `config/auth/create` with `name`, `group_ids` and `local_only`, which creates the
  user without a login. Then `config/auth/update` with `is_active = false` if it is false (create
  does not take it), and `…/homeassistant/create` with `username` and `password` if they are set.
  If a follow-up call fails, the user is deleted again, even if the context was cancelled.
- **Password:** creating a login needs `password` or `password_wo` (read from the config, since
  a write-only value is null in the plan). Without either, an existing login keeps its password.
  Switching from `password` to `password_wo` (with a version) sets the password and removes it
  from state.
- **Read:** `config/auth/list`, find by ID. Missing → remove from state. `username` is the login
  name of the user's `homeassistant` credentials, or null. The password cannot be read: it stays
  as in state while the user has a login, and is null otherwise. `password_wo_version` stays as in
  state.
- **Update:** `config/auth/update` with every setting. Then the login:
  - added → `…/homeassistant/create`;
  - removed → `…/homeassistant/delete` with the old username;
  - username changed → `…/admin_change_username`;
  - password changed → `…/admin_change_password`: `password` differs from state, or `password_wo`
    is set and `password_wo_version` changed. The provider cannot see a change of `password_wo`
    itself. With neither set, the password stays as it is.

  Before adding or removing a login, the provider checks that the user's login in HA is still the
  one the plan expected (none, or the old username), and fails otherwise: a login added outside
  OpenTofu would get a second one before 2026.11, and a username that now belongs to another user
  would delete that user's login. The settings update is sent on every apply; the login steps
  are saved to state one by one, so a failed one is retried by the next apply and the ones before
  it are not.
- **Delete:** `config/auth/delete`, which also deletes the login. A user that is already gone is
  not an error. HA unlinks it from its person.
- **Plan:** creating a login (on a new user, or one without a login) without `password` or
  `password_wo` is an error. A plan that deletes a login warns, naming the username: after an import, a
  configuration without `username` would otherwise delete the login quietly.
- **Import:** by ID or by username; an ID match wins. A configuration that `-generate-config-out`
  writes has `password = null`, which is valid: the password stays as it is.
- **Unmanageable users:** Read and import fail for the owner, who cannot be deleted, and for
  system-generated users (e.g. Supervisor), which HA does not let anyone change. They also fail
  for a user that is not in exactly one system group, because `group` cannot express it.
- **Owner's token:** HA lets only the owner change the username or password of an existing user,
  so those two steps fail with an admin token that is not the owner's. The error says so. Adding
  or removing a login, and every other attribute, needs only an admin token.
- Before 2026.11, `…/homeassistant/create` attaches a second login to a user that has one. The
  provider calls it only when the user has no login in state.

## `homeassistant_person` (generated ID, ADR-0009)

Commands: `person/list|create|update|delete` (key `person_id`). `person/list` returns
`{"storage": [...], "config": [...]}`: the persons in storage, and those defined in YAML.

| Argument | Type | |
|---|---|---|
| `id` | string, optional + computed | `slugify(name)`, or set to choose it (create-then-rename) |
| `name` | string, required | Display name |
| `user_id` | string, optional | The linked user. HA rejects a user that does not exist or is linked to another person |
| `device_trackers` | set of string, default empty | `device_tracker.*` entity IDs. HA checks only the format |
| `picture` | string, optional | URL of the picture |

- **Create and ID:** as for floors, areas and labels ([registries.md](registries.md)): the
  returned ID is checked, and an `id` that is set is created with `name = id`, then renamed.
- **Update:** `person/update` with every field. HA resets `device_trackers` to `[]` when the
  update leaves it out, and keeps `user_id` and `picture`, so unset ones are sent as `null`. This
  holds for the rename of create-then-rename too. Trackers are sent sorted.
- **Read:** list and find by ID in `storage`. An ID in `config` (YAML) is an error, not a
  removal. Missing → remove from state.
- **Import:** by ID. A person defined in YAML cannot be imported.
- **Drift that HA makes:** deleting a user clears `user_id` of its person, and deleting a
  `device_tracker` entity removes it from every person.
