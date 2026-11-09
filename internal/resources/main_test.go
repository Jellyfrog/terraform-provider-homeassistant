package resources_test

import (
	"io"
	"net/url"
	"testing"
	"time"

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
	ha := acctest.SharedInstance(t)
	u, err := url.Parse(ha.URL)
	if err != nil {
		t.Fatal(err)
	}
	c := client.New(client.Config{URL: u, Token: ha.Token, Timeout: 30 * time.Second})
	if ws, ok := c.WS.(io.Closer); ok {
		t.Cleanup(func() { _ = ws.Close() })
	}
	return c
}
