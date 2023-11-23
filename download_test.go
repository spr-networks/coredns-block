package block

import (
	//"os"
	//"strings"
	"fmt"
	"testing"
)

func TestDownload(t *testing.T) {

	b := new(Block)
	b.setupDB("/tmp/download.go")

	db := BoltOpen(b.DbPath + "-staging")
	err := b.dbStagingDownload(db, "https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts", 0)
	if err != nil {
		log.Fatal("failed to download", err)
	}
	fmt.Println("done", getCount(db, gDomainBucket))

	db.Close()
	err = b.transferStagingDB()
	if err != nil {
		log.Fatal("failed x staging", err)
	}

	fmt.Println(gMetrics.BlockedDomains)
}
