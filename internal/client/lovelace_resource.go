package client

import "context"

// LovelaceResource is a Lovelace resource: a URL that the frontend loads on every dashboard, such
// as the JavaScript of a custom card.
type LovelaceResource struct {
	ID string `json:"id"`
	// Type is `module`, `css`, or `js`.
	Type string `json:"type"`
	URL  string `json:"url"`
}

// LovelaceResources returns the Lovelace resources (`lovelace/resources` to list,
// `lovelace/resources/create|update|delete`, naming a resource by `resource_id`). They exist only
// while Lovelace keeps its resources in storage, not in YAML. Create and update take `res_type`
// and `url`.
func LovelaceResources(ws WSCommander) Registry[LovelaceResource] {
	return Registry[LovelaceResource]{
		ws:     ws,
		prefix: "lovelace/resources",
		list:   "lovelace/resources",
		key:    "resource_id",
		idOf:   func(r LovelaceResource) string { return r.ID },
	}
}

// LovelaceResourceMode returns where Lovelace keeps its resources (`lovelace/info`): `storage`, or
// `yaml`, where `lovelace/resources` lists the YAML resources, which have no ID, and the create,
// update and delete commands don't exist.
func LovelaceResourceMode(ctx context.Context, ws WSCommander) (string, error) {
	var info struct {
		ResourceMode string `json:"resource_mode"`
	}
	err := ws.Command(ctx, "lovelace/info", nil, &info)
	return info.ResourceMode, err
}
