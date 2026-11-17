package resources_test

import (
	"testing"

	"github.com/toelke/terraform-provider-homeassistant/internal/acctest"
	"github.com/toelke/terraform-provider-homeassistant/internal/client"
)

func TestMain(m *testing.M) {
	acctest.Main(m)
}

// haClient returns a client for the shared instance, for setting up and checking what the
// provider does. Its WebSocket connection is closed when the test ends.
func haClient(t *testing.T) *client.HAClient {
	t.Helper()
	return acctest.Client(t)
}
