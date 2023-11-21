package block

import (
	"strings"
	"testing"
)

func TestListParse(t *testing.T) {
	var list = `
# 127.0.0.1	example.com
127.0.0.1	example.org	third
008.free-counter.co.uk
com
`

	b := new(Block)

	r := strings.NewReader(list)
	l := make(map[string]DomainValue)
	listRead(r, l, -1)
	b.update = l

	b.domains = make(map[string]DomainValue)
	for entry, _ := range b.update {
		b.domains[entry] = b.update[entry]
	}

	tests := []struct {
		name    string
		blocked bool
	}{
		{"example.org.", false},
		{"example.com.", true},
		{"com.", true},
	}

	for _, test := range tests {
		retIP := ""
		got := b.blocked("1.2.3.4", test.name, &retIP)
		if got != test.blocked {
			t.Errorf("Expected %s to be blocked", test.name)
		}
	}
}
