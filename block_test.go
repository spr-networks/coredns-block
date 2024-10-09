package block

import (
	"os"
	"strings"
	"testing"
)

func TestBlocked(t *testing.T) {
	var list = `
127.0.0.1	005.free-counter.co.uk
127.0.0.1	006.free-adult-counters.x-xtra.com
127.0.0.1	006.free-counter.co.uk
127.0.0.1	007.free-counter.co.uk
127.0.0.1	007.go2cloud.org
008.free-counter.co.uk
com
`

	b := new(Block)
	b.superapi_enabled = true
	os.Remove("/tmp/block_test.db")
	b.setupDB("/tmp/block_test.db")

	r := strings.NewReader(list)
	l := make(map[string]DomainValue)
	listRead(r, l, 0)
	b.update = l

	err := b.UpdateDomains(b.update)
	if err != nil {
		log.Fatal("failed to update block_test -- ", err)
	}

	_, found := b.getDomain("no.exist")
	if found {
		log.Fatal("found missing domain")
	}

	_, found = b.getDomain("007.go2cloud.org.")
	if !found {
		log.Fatal("failed to look up stored domain")
	}

	tests := []struct {
		name    string
		blocked bool
	}{
		{"example.org.", false},
		{"com.", true},

		{"005.free-counter.co.uk.", true},
		{"www.005.free-counter.co.uk.", true},
		{"008.free-counter.co.uk.", true},
		{"www.008.free-counter.co.uk.", true},
	}

	//func (b *Block) blocked(IP string, name string, returnIP *string) bool {

	for _, test := range tests {
		retIP := ""
		retCNAME := ""
		hasPermit := false
		got := b.blocked("1.2.3.4", test.name, &retIP, &retCNAME, &hasPermit)
		if got != test.blocked {
			t.Errorf("Expected %s to be blocked", test.name)
		}
	}
}


func TestOverrides(t *testing.T) {
	var list = `
127.0.0.1	005.free-counter.co.uk
127.0.0.1	006.free-adult-counters.x-xtra.com
127.0.0.1	006.free-counter.co.uk
127.0.0.1	007.free-counter.co.uk
127.0.0.1	007.go2cloud.org
127.0.0.1 override.com
127.0.0.1 ip.permit.com
127.0.0.1 cname.permit.com
008.free-counter.co.uk
com
`

	b := new(Block)
	os.Remove("/tmp/block_test.db")
	b.setupDB("/tmp/block_test.db")

	r := strings.NewReader(list)
	l := make(map[string]DomainValue)
	listRead(r, l, 0)
	b.update = l
	b.superapi_enabled = true

	err := b.UpdateDomains(b.update)
	if err != nil {
		log.Fatal("failed to update block_test -- ", err)
	}

	permit := DomainOverride{"Permit", "override.com.", "", "", "*", 0, []string{}}
	ip_permit := DomainOverride{"Permit", "ip.permit.com.", "1.1.1.1", "", "*", 0, []string{}}
	cname_permit := DomainOverride{"Permit", "cname.permit.com.", "", "safesearch.permit.com", "*", 0, []string{}}

	b.config.PermitDomains = []DomainOverride{permit, ip_permit, cname_permit}

	_, found := b.getDomain("no.exist")
	if found {
		log.Fatal("found missing domain")
	}

	_, found = b.getDomain("007.go2cloud.org.")
	if !found {
		log.Fatal("failed to look up stored domain")
	}

	tests := []struct {
		name    string
		blocked bool
	}{
		{"example.org.", false},
		{"com.", true},

		{"005.free-counter.co.uk.", true},
		{"www.005.free-counter.co.uk.", true},
		{"008.free-counter.co.uk.", true},
		{"www.008.free-counter.co.uk.", true},
		{"override.com.", false},
		{"ip.permit.com.", false},
		{"cname.permit.com.", false},
	}

	//func (b *Block) blocked(IP string, name string, returnIP *string) bool {

	for _, test := range tests {
		retIP := ""
		retCNAME := ""
		hasPermit := false
		got := b.blocked("1.2.3.4", test.name, &retIP, &retCNAME, &hasPermit)
		if got != test.blocked {
			if test.blocked == false {
				t.Errorf("Expected `%s` to be permitted", test.name)
			} else {
				t.Errorf("Expected `%s` to be blocked", test.name)
			}
		}
		if test.name == "ip.permit.com." {
			if retIP != "1.1.1.1" {
				t.Errorf("Expected IP override `%s` to be 1.1.1.1", retIP)
			}
		}

		if test.name == "cname.permit.com." {
			if retCNAME != "safesearch.permit.com" {
				t.Errorf("Expected CNAME override `%s` to be safesearch.permit.com", retCNAME)
			}
		}

	}
}
