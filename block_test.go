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
		got := b.blocked("1.2.3.4", test.name, &retIP)
		if got != test.blocked {
			t.Errorf("Expected %s to be blocked", test.name)
		}
	}
}
