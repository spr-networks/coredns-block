// Package example is a CoreDNS plugin that prints "example" to stdout on every packet received.
//
// It serves as an example CoreDNS plugin with numerous code comments.
package block

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"time"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/metrics"
	clog "github.com/coredns/coredns/plugin/pkg/log"
	"github.com/coredns/coredns/request"

	"github.com/miekg/dns"

	"database/sql"
	_ "modernc.org/sqlite"
)

var log = clog.NewWithPlugin("block")

type BlockMetrics struct {
	TotalQueries   int64
	BlockedQueries int64
	BlockedDomains	 int64
}

var gMetrics = BlockMetrics{}

// Block is the block plugin.
type Block struct {
	update map[string]struct{}
	stop   chan struct{}

	config           SPRBlockConfig
	superapi_enabled bool

	SQL  *sql.DB
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

	returnIP := ""

	gMetrics.TotalQueries++

	if b.blocked(state.IP(), state.Name(), &returnIP) {
		gMetrics.BlockedQueries++

		blockCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
		log.Infof("Blocked %s", state.Name())

		resp := new(dns.Msg)
		resp.SetRcode(r, dns.RcodeNameError)
		w.WriteMsg(resp)

		return dns.RcodeNameError, nil
	}

	// Rewrite a predefined typeA or typeAAAA response
	if returnIP != "" {
		resp := new(dns.Msg)
		resp.SetRcode(r, dns.RcodeSuccess)

		name := r.Question[0].Name
		rrType := r.Question[0].Qtype

		if rrType == dns.TypeA {
			ans := &dns.A{
				Hdr: dns.RR_Header{
					Name:   name,
					Rrtype: rrType,
					Class:  dns.ClassINET,
					Ttl:    1,
				},
				A: net.ParseIP(returnIP),
			}

			resp.Answer = append(resp.Answer, ans)
			w.WriteMsg(resp)
			return dns.RcodeSuccess, nil
		} else if rrType == dns.TypeAAAA {
			ans := &dns.AAAA{
				Hdr: dns.RR_Header{
					Name:   name,
					Rrtype: rrType,
					Class:  dns.ClassINET,
					Ttl:    1,
				},
				AAAA: net.ParseIP(returnIP),
			}

			resp.Answer = append(resp.Answer, ans)
			w.WriteMsg(resp)
			return dns.RcodeSuccess, nil
		}
	}

	return plugin.NextOrFailure(b.Name(), b.Next, ctx, w, r)
}

// Name implements the Handler interface.
func (b *Block) Name() string { return "block" }

func matchOverride(IP string, name string, overrides []DomainOverride, returnIP *string) bool {

	cur_time := time.Now().Unix()

	for _, entry := range overrides {
		if entry.Expiration != 0 {
			//this override has expired
			if entry.Expiration <= cur_time {
				continue
			}
		}

		if entry.ClientIP == "" || entry.ClientIP == "*" || entry.ClientIP == IP {
			//match wildcard or match IP
			//now check if domain matches name to make a decision
			if name == entry.Domain {
				//got a match -> if there is a returnIP set, carry it over
				if entry.ResultIP != "" {
					*returnIP = entry.ResultIP
				}
				return true
			}

		}
	}

	return false
}

func (b *Block) dumpEntries(w http.ResponseWriter, r *http.Request) {
	sql_query_domains := `
        SELECT domain FROM domains WHERE enabled=1`

	rows, err := b.SQL.Query(sql_query_domains)

	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	domains := []string{}

	defer rows.Close()
	for rows.Next() {
		var domain string
		err := rows.Scan(&domain)
		if err == nil {
			domains = append(domains, domain)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(domains)
}

func (b *Block) checkBlock(IP string, name string, returnIP *string) bool {

	if b.superapi_enabled {
		// do not block for excluded IPs
		for _, excludeIP := range b.config.ClientIPExclusions {
			if IP == excludeIP {
				//not blocked
				return false
			}
		}

		if matchOverride(IP, name, b.config.PermitDomains, returnIP) {
			//permit this domain
			return false
		}

		if matchOverride(IP, name, b.config.BlockDomains, returnIP) {
			//yes blocked
			return true
		}

	}

	sql_query_domain := `
        SELECT id FROM domains WHERE enabled=1 and domain=?`

	r := b.SQL.QueryRow(sql_query_domain, name)

	var (
		id int64
	)

	switch err := r.Scan(&id); err {
	case nil:
		return true
	}
	return false
}

func (b *Block) blocked(IP string, name string, returnIP *string) bool {

	if b.checkBlock(IP, name, returnIP) {
		return true
	}

	i, end := dns.NextLabel(name, 0)
	for !end {
		if b.checkBlock(IP, name[i:], returnIP) {
			return true
		}
		i, end = dns.NextLabel(name, i)
	}
	return false
}

func (b *Block) setupDB(filename string) {

	db, err := sql.Open("sqlite", filename)
	if err != nil {
		panic(err)
	}
	if db == nil {
		panic("db nil")
	}

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
