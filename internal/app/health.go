package app

import (
	"fmt"
	"net"
	"net/http"
	"time"
)

// Health asks a running agent whether it is answering, and is what
// `agent -health` runs. A container has to be able to report on the process
// inside it; doing that with curl means keeping a network client in the runtime
// image for one request the agent can make of itself.
//
// addr is the listen address the agent was configured with, which may name no
// host at all (":8080") or a wildcard one ("0.0.0.0:8080"). Either way the
// request goes to the loopback, because it is made from inside the container.
func Health(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("address %q: %w", addr, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	url := "http://" + net.JoinHostPort(host, port) + "/status"

	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("%s: %w", url, err)
	}
	defer resp.Body.Close()
	// /status answers without reaching a model, so anything but 200 means the
	// process is up and not serving.
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: status %d", url, resp.StatusCode)
	}
	return nil
}
