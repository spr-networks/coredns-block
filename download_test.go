package block

import (
	//"os"
	//"strings
	"fmt"
	"runtime"
	"testing"
)

func TestDownload(t *testing.T) {

	b := new(Block)
	b.setupDB("/tmp/download.db")
	b.superapi_enabled = true
	db := BoltOpen(b.DbPath + "-staging")

	lists := []string{"https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts",
		"https://raw.githubusercontent.com/blocklistproject/Lists/master/ads.txt",
		"https://raw.githubusercontent.com/blocklistproject/Lists/master/tracking.txt",
		"https://raw.githubusercontent.com/blocklistproject/Lists/master/malware.txt",
		"https://raw.githubusercontent.com/blocklistproject/Lists/master/porn.txt"}

	for i, entry := range lists {
		err := b.dbStagingDownload(db, entry, i)
		if err != nil {
			log.Fatal("failed to download", err)
		}
	}

	fmt.Println("done", getCount(db, gDomainBucket))

	db.Close()
	err := b.transferStagingDB()
	if err != nil {
		log.Fatal("failed x staging", err)
	}

	fmt.Println(gMetrics.BlockedDomains)

	runtime.GC()

	printMemUsage(t)
}

func printMemUsage(t *testing.T) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	//run with -test.v to see the memory use

	t.Logf("Alloc = %v MiB", bToMb(m.Alloc))
	t.Logf("\tTotalAlloc = %v MiB", bToMb(m.TotalAlloc))
	t.Logf("\tSys = %v MiB", bToMb(m.Sys))
	t.Logf("\tNumGC = %v\n", m.NumGC)
}

func bToMb(b uint64) uint64 {
	return b / 1024 / 1024
}
