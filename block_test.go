package block

import (
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

	b.setupDB(":memory:")

	r := strings.NewReader(list)
	l := make(map[string]DomainValue)
	listRead(r, l, -1)
	b.update = l

	tx, err := b.SQL.Begin()
	if err != nil {
		log.Fatal(err)
	}

	tx.Exec("DELETE FROM domains")

	// Update the sqlite database
	stmt, err := tx.Prepare("INSERT INTO domains(domain) VALUES(?)")
	if err != nil {
		log.Fatal(err)
	}

	for domain, _ := range b.update {
		stmt.Exec(domain)
	}

	tx.Commit()

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

	for _, test := range tests {
		got := b.blocked(test.name)
		if got != test.blocked {
			t.Errorf("Expected %s to be blocked", test.name)
		}
	}
}
