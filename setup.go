package block

import (
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"

	"github.com/coredns/caddy"

	"sync"
	"time"
)

var doOnce sync.Once

var super_api = false

var gDbPath = TEST_PREFIX + "/state/dns/dns.db"

func init() { plugin.Register("block", setup) }

func setup(c *caddy.Controller) error {
	superapi_enabled := false
	c.Next()
	if c.NextArg() {
		arg := c.Val()
		if arg == "enable_superapi" {
			superapi_enabled = true
		} else {
			return plugin.Error("block", c.ArgErr())
		}
	}

	block := New()
	block.superapi_enabled = superapi_enabled

	c.OnStartup(func() error {

		block.setupDB(gDbPath)

		doOnce.Do(func() {
			//Multiple server instances could be running, but the plugin only needs
			//one instance to download and refresh the list
			go func() {
				//spr is enabled
				if block.superapi_enabled {
					block.loadSPRConfig()
				}

				if block.superapi_enabled {
					block.runAPI()
				}

				retries := 3
				for retries > 0 {
					block.download()
					time.Sleep(time.Second * 10)
					if !block.ShouldRetryRefresh() {
						break
					}
					retries--
					time.Sleep(time.Minute * 5)
				}

			}()

			go func() { block.refresh() }()
			go func() { block.refreshTags() }()

		})

		return nil
	})

	c.OnShutdown(func() error {
		close(block.stop)
		return nil
	})

	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		block.Next = next
		return block
	})

	return nil
}
