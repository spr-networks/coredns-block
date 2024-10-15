package block

import (
	//"os"
	//"strings
	//	"fmt"
	"testing"
)

func TestCategory(t *testing.T) {
	b := new(Block)
	b.setupDB("/tmp/category.db")
	b.superapi_enabled = true

	b.config.BlockLists = []ListEntry{
		{"https://raw.githubusercontent.com/blocklistproject/Lists/master/twitter.txt",
			true,
			[]string{},
			"social",
			true},
		{"https://raw.githubusercontent.com/blocklistproject/Lists/master/facebook.txt",
			true,
			[]string{},
			"social",
			true},
		{"https://raw.githubusercontent.com/blocklistproject/Lists/master/ads.txt",
			true,
			[]string{},
			"ads",
			false},
	}

	for i, entry := range b.config.BlockLists {
		err := b.dbStagingDownload(b.Db, entry.URI, i)
		if err != nil {
			log.Fatal("failed to download", err)
		}
	}

	retIP := ""
	retCNAME := ""
	categories := []string{}
	hasPermit := false
	isBlocked := b.blocked("1.2.3.4", "twitter.com.", &retIP, &retCNAME, &hasPermit, &categories)
	if isBlocked != false {
		t.Errorf("Expected `twitter.com` to be permitted")
	}
	if hasPermit != false {
		t.Errorf("Did not expect permit override")
	}
	if len(categories) != 1 && categories[0] != "social" {
		t.Errorf("Expected social category")
	}

	isBlocked = b.blocked("1.2.3.4", "1-1ads.com.", &retIP, &retCNAME, &hasPermit, &categories)
	if isBlocked != true {
		t.Errorf("Expected `1-1ads.com` to be blocked")
	}

}
