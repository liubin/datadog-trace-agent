package statsd

import (
	"fmt"

	"github.com/DataDog/datadog-go/statsd"
	"github.com/DataDog/datadog-trace-agent/config"
)

// Client is a global Statsd client. When a client is configured via Configure,
// that becomes the new global Statsd client in the package.
var Client *statsd.Client

// Configure creates a statsd client from a dogweb.ini style config file and set it to the global Statsd.
func Configure(conf *config.AgentConfig) error {
	client, err := statsd.New(fmt.Sprintf("%s:%d", conf.StatsdHost, conf.StatsdPort))
	if err != nil {
		return err
	}

	Client = client
	return nil
}
