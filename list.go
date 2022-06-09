package block

import (
	"bufio"
	"io"
	"strings"

	"github.com/miekg/dns"
)


func listRead(r io.Reader, list map[string]DomainValue, list_id int64) error {
	var ignoreDomains = [...]string{"localhost.", "localhost.localdomain.", "local.", "broadcasthost.", "localhost.", "ip6-localhost.", "ip6-loopback.", "localhost.", "ip6-localnet.", "ip6-mcastprefix.", "ip6-allnodes.", "ip6-allrouters.", "ip6-allhosts.", "0.0.0.0"}

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		txt := scanner.Text()
		if strings.HasPrefix("#", txt) {
			continue
		}
		var domain string
		flds := strings.Fields(scanner.Text())
		switch len(flds) {
		case 1:
			domain = dns.Fqdn(flds[0])
		case 2:
			domain = dns.Fqdn(flds[1])
		}

		list[domain] = DomainValue{list_id: list_id}
	}

	for _, k := range ignoreDomains {
		delete(list, k)
	}


	return scanner.Err()
}
