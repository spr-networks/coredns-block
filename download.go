package block

import (
	_ "modernc.org/sqlite"
	"net/http"
	"time"
)

// our default block lists.
var blocklist = []string{"https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts"}

func (b *Block) download() {
	domains := 0

	if b.superapi_enabled {
		//override blocklist with config
		blocklist = []string{}
		BLmtx.RLock()
		for _, entry := range b.config.BlockLists {
			if entry.Enabled {
				blocklist = append(blocklist, entry.URI)
			}
		}
		BLmtx.RUnlock()
	}


	list_id := int64(0)
	for _, url := range blocklist {
		log.Infof("Block list update started %q", url)
		resp, err := http.Get(url)
		if err != nil {
			log.Warningf("Failed to download block list %q: %s", url, err)
			continue
		}
		if err := listRead(resp.Body, b.update, list_id); err != nil {
			log.Warningf("Failed to parse block list %q: %s", url, err)
		}
		domains += len(b.update)
		resp.Body.Close()

		log.Infof("Block list update finished %q", url)
		list_id += 1
	}

	log.Infof("Updating database with new domains")

	tx, err := b.SQL.Begin()
	if err != nil {
		log.Fatal(err)
	}

	tx.Exec("DELETE FROM domains")

	// Update the sqlite database
	stmt, err := tx.Prepare("INSERT INTO domains(domain, list_id) VALUES(?, ?)")
  if err != nil {
        log.Fatal(err)
  }

	for domain, val := range b.update {
		stmt.Exec(domain, val.list_id)
	}

	stmt.Close()

	tx.Commit()
	b.update = make(map[string]DomainValue)

	b.SQL.Exec("VACUUM")

	gMetrics.BlockedDomains = int64(domains)
	log.Infof("Block lists updated: %d domains added", domains)
}

func (b *Block) refresh() {
	tick := time.NewTicker(1 * week)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			b.download()

		case <-b.stop:
			return
		}
	}
}

const week = time.Hour * 24 * 7
