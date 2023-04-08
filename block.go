// Package example is a CoreDNS plugin that prints "example" to stdout on every packet received.
//
// It serves as an example CoreDNS plugin with numerous code comments.
package block

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/metrics"
	clog "github.com/coredns/coredns/plugin/pkg/log"
	"github.com/coredns/coredns/request"

	"github.com/miekg/dns"

	"database/sql"
	"github.com/spr-networks/sprbus"
	_ "modernc.org/sqlite"
)

var log = clog.NewWithPlugin("block")

type BlockMetrics struct {
	TotalQueries   int64
	BlockedQueries int64
	BlockedDomains int64
}

var gMetrics = BlockMetrics{}

type DomainValue struct {
	list_id int64
}

// Block is the block plugin.
type Block struct {
	update map[string]DomainValue
	stop   chan struct{}

	config           SPRBlockConfig
	superapi_enabled bool

	SQL  *sql.DB
	Next plugin.Handler
}

func New() *Block {
	return &Block{
		update: make(map[string]DomainValue),
		stop:   make(chan struct{}),
	}
}

type DNSBlockEvent struct {
	ClientIP string
	Name     string
}

type DNSOverrideEvent struct {
	ClientIP string
	IP       string // the new IP response
	Name     string
}

func (i *DNSBlockEvent) String() string {
	x, _ := json.Marshal(i)
	return string(x)
}

func (i *DNSOverrideEvent) String() string {
	x, _ := json.Marshal(i)
	return string(x)
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

		event := DNSBlockEvent{state.IP(), state.Name()}
		sprbus.PublishString("dns:block:event", event.String())

		return dns.RcodeNameError, nil
	}

	// Rewrite a predefined typeA or typeAAAA response
	if returnIP != "" {
		resp := new(dns.Msg)
		resp.SetRcode(r, dns.RcodeSuccess)

		name := r.Question[0].Name
		rrType := r.Question[0].Qtype

		event := DNSOverrideEvent{state.IP(), returnIP, name}
		sprbus.PublishString("dns:override:event", event.String())

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

				if len(entry.Tags) > 0 {
					//tags were specified, make sure that the IP has one of those set
					return IPHasTags(entry.ClientIP, entry.Tags)
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

var IPTagMap = make(map[string][]string)

var IPTagmtx sync.RWMutex

type DeviceEntry struct {
	Name       string
	MAC        string
	WGPubKey   string
	VLANTag    string
	RecentIP   string
	PSKEntry   PSKEntry
	Groups     []string
	DeviceTags []string
}

type PSKEntry struct {
	Type string
	Psk  string
}

var DevicesConfigPath = TEST_PREFIX + "/configs/devices/"
var DevicesPublicConfigFile = TEST_PREFIX + "/state/public/devices-public.json"

func APIDevices() (map[string]DeviceEntry, error) {
	devs := map[string]DeviceEntry{}

	data, err := ioutil.ReadFile(DevicesPublicConfigFile)
	if err == nil {
		err = json.Unmarshal(data, &devs)
		if err != nil {
			fmt.Println(err)
			return nil, err
		}
	} else {
		fmt.Println(err)
		return nil, err
	}

	return devs, nil
}

func (b *Block) updateIPTags() {
	newMap := make(map[string][]string)

	devices, err := APIDevices()
	if err != nil {
		//something failed, stop processing
		return
	}

	for _, entry := range devices {
		if entry.RecentIP != "" {
			newMap[entry.RecentIP] = entry.DeviceTags
		}
	}

	IPTagmtx.Lock()
	IPTagMap = newMap
	IPTagmtx.Unlock()
}

func (b *Block) refreshTags() {
	b.updateIPTags()

	tick := time.NewTicker(1 * time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			b.updateIPTags()
		case <-b.stop:
			return
		}
	}
}

func IPHasTags(IP string, applied_tags []string) bool {

	if len(applied_tags) == 0 {
		return false
	}

	IPTagmtx.RLock()
	device_tags, exists := IPTagMap[IP]
	IPTagmtx.RUnlock()
	if !exists {
		//IP not mapped as having tags, return false
		return false
	}

	for _, applied_tag := range applied_tags {
		for _, device_tag := range device_tags {
			if applied_tag == device_tag {
				return true
			}
		}
	}

	return false
}

func (b *Block) deviceMatchBlockListTags(IP string, list_id int64) bool {
	// a domain was blocked. Check if the list_id has a group specification.
	// return true if there is no group specification, or the device is
	// in the specified. If the device is not in a specified group, return false

	BLmtx.RLock()
	defer BLmtx.RUnlock()

	if list_id >= 0 && int(list_id) < len(b.config.BlockLists) {
		applied_tags := b.config.BlockLists[list_id].Tags

		if len(applied_tags) == 0 {
			//no tags specified, succeed by default
			return true
		}

		//had tags, return true only if IP has that tag. otherwise false
		return IPHasTags(IP, applied_tags)
	}

	//invalid list_id, succeed
	return true
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
        SELECT id, list_id FROM domains WHERE enabled=1 and domain=?`

	r := b.SQL.QueryRow(sql_query_domain, name)

	var (
		id      int64
		list_id int64
	)

	switch err := r.Scan(&id, &list_id); err {
	case nil:
		if b.superapi_enabled {
			return b.deviceMatchBlockListTags(IP, list_id)
		}
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

	db.Exec("PRAGMA journal_mode=WAL;")

	domainsSchema := `CREATE TABLE IF NOT EXISTS domains
	(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		list_id INTEGER,
		type INTEGER NOT NULL DEFAULT 0,
		domain TEXT NOT NULL,
		enabled BOOLEAN NOT NULL DEFAULT 1,
		date_added INTEGER NOT NULL DEFAULT (cast(strftime('%s', 'now') as int)),
		date_modified INTEGER NOT NULL DEFAULT (cast(strftime('%s', 'now') as int)),
		comment TEXT,
		UNIQUE(domain, list_id, type)
	);`

	if _, err = db.Exec(domainsSchema); nil != err {
		panic(err)
	}

	b.SQL = db

}
