// Package example is a CoreDNS plugin that prints "example" to stdout on every packet received.
//
// It serves as an example CoreDNS plugin with numerous code comments.
package block

import (
	"context"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/metrics"
	clog "github.com/coredns/coredns/plugin/pkg/log"
	"github.com/coredns/coredns/request"

	"github.com/miekg/dns"

	"database/sql"
	_ "modernc.org/sqlite"
)

var log = clog.NewWithPlugin("block")

// Block is the block plugin.
type Block struct {
	update map[string]struct{}
	stop chan struct{}

	SQL *sql.DB
	Next plugin.Handler
}

func New() *Block {
	return &Block{
		update: make(map[string]struct{}),
		stop:   make(chan struct{}),
	}
}

// ServeDNS implements the plugin.Handler interface.
func (b *Block) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	state := request.Request{W: w, Req: r}
	if b.blocked(state.Name()) {
		blockCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
		log.Infof("Blocked %s", state.Name())

		resp := new(dns.Msg)
		resp.SetRcode(r, dns.RcodeNameError)
		w.WriteMsg(resp)

		return dns.RcodeNameError, nil
	}

	return plugin.NextOrFailure(b.Name(), b.Next, ctx, w, r)
}

// Name implements the Handler interface.
func (b *Block) Name() string { return "block" }

func (b *Block) checkBlock(name string) bool {

	sql_query_domain := `
        SELECT id FROM domains WHERE enabled=1 and domain=?`

  r := b.SQL.QueryRow(sql_query_domain, name);

  var (id int64)

	switch err := r.Scan(&id); err {
	case nil:
		return true
	}
	return false
}

func (b *Block) blocked(name string) bool {

	if b.checkBlock(name) {
		return true
	}

	i, end := dns.NextLabel(name, 0)
	for !end {
		if b.checkBlock(name[i:]) {
			return true
		}
		i, end = dns.NextLabel(name, i)
	}
	return false
}

func (b *Block) setupDB(filename string) {

	db, err := sql.Open("sqlite",  filename)
	if err != nil { panic(err) }
	if db == nil { panic("db nil") }

	domainsSchema := `CREATE TABLE IF NOT EXISTS domains
	(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		type INTEGER NOT NULL DEFAULT 0,
		domain TEXT NOT NULL,
		enabled BOOLEAN NOT NULL DEFAULT 1,
		date_added INTEGER NOT NULL DEFAULT (cast(strftime('%s', 'now') as int)),
		date_modified INTEGER NOT NULL DEFAULT (cast(strftime('%s', 'now') as int)),
		comment TEXT,
		UNIQUE(domain, type)
	);`

	if _, err = db.Exec(domainsSchema); nil != err {
		panic(err)
	}

	b.SQL = db

}
