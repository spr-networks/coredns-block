package block

import (
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"

	"github.com/coredns/caddy"

	"sync"
)

var doOnce sync.Once

func init() { plugin.Register("block", setup) }

func setup(c *caddy.Controller) error {
	c.Next()
	if c.NextArg() {
		return plugin.Error("block", c.ArgErr())
	}

	block := New()

	c.OnStartup(func() error {

		block.setupDB("dns.db?_journal_mode=WAL")

		doOnce.Do(func() {
			//Multiple server instances could be running, but the plugin only needs
			//one instance to download and refresh the list
			go func() { block.download() }()
			go func() { block.refresh() }()
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
