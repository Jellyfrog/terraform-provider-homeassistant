package client

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
