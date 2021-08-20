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
	b.setupDB(":memory:")
	r := strings.NewReader(list)
	l := make(map[string]struct{})
	listRead(r, l)
	b.update = l

	tx, err := b.SQL.Begin()
	if err != nil {
		log.Fatal(err)
	}

	tx.Exec("DELETE FROM domains");

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
		{"example.com.", true},
		{"com.", true},
	}

	for _, test := range tests {
		got := b.blocked(test.name)
		if got != test.blocked {
			t.Errorf("Expected %s to be blocked", test.name)
		}
	}
}
