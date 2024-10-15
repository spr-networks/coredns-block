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
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/metrics"
	clog "github.com/coredns/coredns/plugin/pkg/log"
	"github.com/coredns/coredns/request"

	"github.com/miekg/dns"

	"github.com/spr-networks/sprbus"
)

import bolt "go.etcd.io/bbolt"

var log = clog.NewWithPlugin("block")
var gDomainBucket = "domains"

type BlockMetrics struct {
	TotalQueries   int64
	BlockedQueries int64
	BlockedDomains int64
}

var gMetrics = BlockMetrics{}

type DomainValue struct {
	List_ids []int
	Disabled bool
}

var Dmtx sync.RWMutex
var Stagemtx sync.RWMutex

// Block is the block plugin.
type Block struct {
	update map[string]DomainValue
	stop   chan struct{}

	config           SPRBlockConfig
	superapi_enabled bool

	Db     *bolt.DB
	DbPath string
	Next   plugin.Handler
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

// rebinding code
type EventData struct {
	Q []dns.Question
	A []dns.RR
}

type DNSEvent struct {
	dns.ResponseWriter
	data       EventData
	delayedMsg *dns.Msg
}

func (i *DNSEvent) Write(b []byte) (int, error) {
	return i.ResponseWriter.Write(b)
}

func (i *DNSEvent) WriteMsg(m *dns.Msg) error {
	i.data.Q = m.Question
	i.data.A = m.Answer

	//delay the message until a decision has been made
	i.delayedMsg = m

	return nil
}

func (i *DNSEvent) String() string {
	x, _ := json.Marshal(i.data)
	return string(x)
}

type ResponseWriterDelay struct {
	dns.ResponseWriter
}

type DNSBlockRebindingEvent struct {
	ClientIP  string
	BlockedIP string
	Name      string
}

func (i *DNSBlockRebindingEvent) String() string {
	x, _ := json.Marshal(i)
	return string(x)
}

// ServeDNS implements the plugin.Handler interface.
func (b *Block) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	state := request.Request{W: w, Req: r}

	returnIP := ""
	returnCNAME := ""
	categories := []string{}
	hasPermit := false

	gMetrics.TotalQueries++
	clientIP := state.IP()

	clientDnsPolicies := b.getClientDnsPolicies(clientIP)
	if len(clientDnsPolicies) > 0 {
		ctx = context.WithValue(ctx, "DNSPolicies", clientDnsPolicies)
	}

	if b.blocked(clientIP, state.Name(), &returnIP, &returnCNAME, &hasPermit, &categories) {
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

	if len(categories) > 0 {
		//Add DNSCategories to the request context for log, forward.
		ctx = context.WithValue(ctx, "DNSCategories", categories)
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
			err := w.WriteMsg(resp)
			if err != nil {
				return dns.RcodeNameError, err
			}

			return dns.RcodeSuccess, nil
		}
	} else if returnCNAME != "" {
		resp := new(dns.Msg)
		resp.SetRcode(r, dns.RcodeSuccess)

		name := r.Question[0].Name

		event := DNSOverrideEvent{state.IP(), returnCNAME, name}
		sprbus.PublishString("dns:override:event", event.String())

		cname := &dns.CNAME{
			Hdr: dns.RR_Header{
				Name:   name,
				Rrtype: dns.TypeCNAME,
				Class:  dns.ClassINET,
				Ttl:    1,
			},
			Target: returnCNAME,
		}

		resp.Answer = append(resp.Answer, cname)
		err := w.WriteMsg(resp)
		if err != nil {
			return dns.RcodeNameError, err
		}
		return dns.RcodeSuccess, nil
	}

	//now we do a rebinding check if hasPermit is false
	// when a permit override has been set, we ignore dns rebinding
	// also make sure RebindingCheckDisable is false
	if !hasPermit && !b.config.RebindingCheckDisable {
		resolve_event := &DNSEvent{
			ResponseWriter: w,
		}

		//resolve the IP then check the result
		c, err := b.Next.ServeDNS(ctx, resolve_event, r)
		if err != nil {
			//failed out early.
			return c, err
		}

		for _, answer := range resolve_event.data.A {
			answerString := answer.String()
			parts := strings.Split(answerString, "\t")
			if len(parts) > 2 {
				rr_type := parts[len(parts)-2]
				if rr_type == "A" || rr_type == "AAAA" {
					ip := net.ParseIP(parts[len(parts)-1])
					if ip != nil && b.isRebindingIP(ip) {
						//we should block this now
						resp := new(dns.Msg)
						resp.SetRcode(r, dns.RcodeNameError)
						w.WriteMsg(resp)

						bus_event := DNSBlockRebindingEvent{state.IP(), ip.String(), state.Name()}
						sprbus.PublishString("dns:blockrebind:event", bus_event.String())

						return dns.RcodeNameError, nil
					}
				}
			}
		}

		// fell thru, return the event.
		resolve_event.ResponseWriter.WriteMsg(resolve_event.delayedMsg)
		return c, err

	} else {
		//fall through
		return plugin.NextOrFailure(b.Name(), b.Next, ctx, w, r)
	}

}

// Name implements the Handler interface.
func (b *Block) Name() string { return "block" }

func matchOverride(IP string, fullname string, name string, overrides []DomainOverride, returnIP *string, returnCNAME *string) bool {

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
			if name == entry.Domain || fullname == entry.Domain {
				//got a match -> set results if available
				if entry.ResultIP != "" {
					*returnIP = entry.ResultIP
				}
				if entry.ResultCNAME != "" {
					*returnCNAME = entry.ResultCNAME
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
	domains := []string{}

	Dmtx.Lock()

	err, items := getItems(b.Db, gDomainBucket)
	if err != nil {
		for _, v := range items {
			domains = append(domains, v.Key)
		}
	}

	Dmtx.Unlock()

	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(domains)
}

var IPTagMap = make(map[string][]string)
var IPPolicyMap = make(map[string][]string)

var IPTagmtx sync.RWMutex

type DeviceEntry struct {
	Name       string
	MAC        string
	WGPubKey   string
	VLANTag    string
	RecentIP   string
	PSKEntry   PSKEntry
	Policies   []string //tbd: dns quarantine mode in the future?
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
	newPolicyMap := make(map[string][]string)

	devices, err := APIDevices()
	if err != nil {
		//something failed, stop processing
		return
	}

	for _, entry := range devices {
		if entry.RecentIP != "" {
			newMap[entry.RecentIP] = entry.DeviceTags
			newPolicyMap[entry.RecentIP] = entry.Policies
		}
	}

	IPTagmtx.Lock()
	IPTagMap = newMap
	IPPolicyMap = newPolicyMap
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

func IPQuarantined(IP string) bool {
	IPTagmtx.RLock()
	policies, policy_exists := IPPolicyMap[IP]
	IPTagmtx.RUnlock()

	if policy_exists {
		return slices.Contains(policies, "quarantine")
	}
	return false
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

func (b *Block) deviceMatchBlockListTags(IP string, entry DomainValue, block bool) bool {
	// a domain was blocked. Check if the list_id has a group specification.
	// return true if there is no group specification, or the device is
	// in the specified. If the device is not in a specified group, return false
	BLmtx.RLock()
	defer BLmtx.RUnlock()

	for _, list_id := range entry.List_ids {

		if list_id >= 0 && int(list_id) < len(b.config.BlockLists) {
			if b.config.BlockLists[list_id].DontBlock == true {
				continue
			}

			applied_tags := b.config.BlockLists[list_id].Tags

			if len(applied_tags) == 0 {
				//no tags specified, continue
				continue
			}

			//had tags, return true only if IP has that tag. otherwise false
			return IPHasTags(IP, applied_tags)
		}

	}
	//no list
	return block
}

func (b *Block) getDomain(name string) (DomainValue, bool) {
	err, item := getItem(b.Db, gDomainBucket, name)
	if err == nil {
		return item.Value, true
	}
	return DomainValue{}, false
}

func (b *Block) getDomainInfo(name string) (DomainValue, []string, bool, bool) {
	entry, exists := b.getDomain(name)
	categories := []string{}
	if exists {

		//if all of the lists are set to DontBlock, then dont block it
		dontBlock := true
		sawList := false
		BLmtx.RLock()
		//get the categories from the list ids
		for _, list_id := range entry.List_ids {
			if list_id >= 0 && int(list_id) < len(b.config.BlockLists) {
				sawList = true
				cat := b.config.BlockLists[list_id].Category
				if cat != "" && !slices.Contains(categories, cat) {
					categories = append(categories, cat)
				}
				dontBlock = dontBlock && b.config.BlockLists[list_id].DontBlock
			}
		}
		BLmtx.RUnlock()
		//if no lists were valid assume blocking behavior.
		if sawList == false {
			dontBlock = false
		}
		return entry, categories, !dontBlock, true
	}

	return DomainValue{}, categories, false, false
}

func (b *Block) isRebindingIP(ip net.IP) bool {
	// Need to block zero addresses as well
	_, zeroipv4, _ := net.ParseCIDR("0.0.0.0/32")
	_, zeroipv6, _ := net.ParseCIDR("::/32")

	if ip.IsPrivate() || ip.IsLoopback() || ip.IsMulticast() ||
		ip.IsInterfaceLocalMulticast() || zeroipv4.Contains(ip) || zeroipv6.Contains(ip) {
		log.Infof("Blocking forward of %s, a local/private/multicast/zero IP address", ip.String())
		return true
	}

	return false
}

func (b *Block) checkBlock(IP string, name string, fullname string, returnIP *string, returnCNAME *string, hasPermit *bool, categories *[]string) bool {
	*hasPermit = false
	if b.superapi_enabled {
		// do not block for excluded IPs
		for _, excludeIP := range b.config.ClientIPExclusions {
			if IP == excludeIP {
				//not blocked
				return false
			}
		}

		if IPQuarantined(IP) {
			//in quarantine mode, send all traffic to the QuarantineHostIP
			if b.config.QuarantineHostIP != "" {
				*hasPermit = true
				*returnIP = b.config.QuarantineHostIP
				return false
			}
			//otherwise block the DNS lookup.
			return true
		}

		if matchOverride(IP, fullname, name, b.config.PermitDomains, returnIP, returnCNAME) {
			*hasPermit = true
			//permit this domain
			return false
		}

		if matchOverride(IP, fullname, name, b.config.BlockDomains, returnIP, returnCNAME) {
			//yes blocked
			return true
		}

	}

	Dmtx.RLock()
	entry, blockCategories, block, exists := b.getDomainInfo(name)
	Dmtx.RUnlock()
	if exists && !entry.Disabled {
		if len(blockCategories) > 0 {
			*categories = blockCategories
		}
		if b.superapi_enabled {
			return b.deviceMatchBlockListTags(IP, entry, block)
		}
		return block
	}

	return false
}

func (b *Block) blocked(IP string, name string, returnIP *string, returnCNAME *string, hasPermit *bool, categories *[]string) bool {

	if b.checkBlock(IP, name, name, returnIP, returnCNAME, hasPermit, categories) {
		return true
	}

	i, end := dns.NextLabel(name, 0)
	for !end {
		if b.checkBlock(IP, name[i:], name, returnIP, returnCNAME, hasPermit, categories) {
			return true
		}
		i, end = dns.NextLabel(name, i)
	}

	return false
}

func (b *Block) getClientDnsPolicies(IP string) []string {
	ret := []string{}

	IPTagmtx.RLock()
	policies, policy_exists := IPPolicyMap[IP]
	IPTagmtx.RUnlock()

	//capture policies with the dns: prefix
	if policy_exists {
		for _, entry := range policies {
			if strings.HasPrefix(entry, "dns:") {
				ret = append(ret, entry)
			}
		}
	}

	return ret
}

func (b *Block) setupDB(filename string) {

	Dmtx.Lock()
	defer Dmtx.Unlock()

	b.Db = BoltOpen(filename)
	b.DbPath = filename

	gMetrics.BlockedDomains = getCount(b.Db, gDomainBucket)
}
