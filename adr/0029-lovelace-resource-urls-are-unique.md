# 29. Creating or updating a Lovelace resource fails if another one has its URL

Date: 2026-11-09 · Status: Accepted · Like ADR-0024

## Context

`homeassistant_lovelace_resource` (PR #95, issue #91) manages a Lovelace resource: a URL the
frontend loads on every dashboard. Home Assistant identifies a resource by a random `id` and
accepts any number of resources with the same `url`. The frontend then loads the file once per
resource, which for a custom card's module means a second `customElements.define` and errors in
the browser console.

A user who starts managing an existing setup usually has the resource already, added by hand or
by HACS. Applying a configuration with the same URL would silently add a duplicate instead of
taking over the existing one.

The options put to the maintainer on PR #95:

1. Create and update fail when another resource has exactly the URL; the error names its ID for
   `tofu import`.
2. No check: create whatever is configured, as Home Assistant does.
3. Create anyway, with a warning that names the other resource.

The maintainer chose option 1, in line with ADR-0024 for automations, scripts and scenes.

## Decision

Before `lovelace/resources/create` and `lovelace/resources/update`, the provider lists the
resources. If a resource other than the one being updated has exactly the planned `url`, the
operation fails with an error on `url` that names that resource's ID and suggests `tofu import`.

Import accepts the ID or the URL. A URL that several resources share is an error that asks for the
ID.

## Consequences

- Applying a configuration never adds a second resource with a URL that one already has, except
  when separate runs race to create the same new URL (see below).
- The comparison is exact. HACS registers its resources with a `?hacstag=…` query, so
  `/hacsfiles/button-card/button-card.js` in the configuration does not match HACS's
  `/hacsfiles/button-card/button-card.js?hacstag=…`, and the frontend loads both. Import HACS's
  resource by its URL instead of declaring a new one.
- A create or update costs one extra `lovelace/resources` call.
- The provider holds a lock from the check until the create or update returns, so two resources
  with the same URL in one run cannot both pass. Two Tofu runs, or Tofu and the UI at the same
  time, still can: Home Assistant offers no atomic check.
