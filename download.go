package block

import (
	"net/http"
	"sync"
	"time"
)

// our default block lists.
var blocklist = []string{"https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts"}

var DLmtx sync.Mutex

func (b *Block) download() {

	go func() {
		DLmtx.Lock()
		defer DLmtx.Unlock()

		domains := 0
		list_ids := []int{}
		if b.superapi_enabled {
			//override blocklist with config
			blocklist = []string{}
			BLmtx.RLock()
			for i, entry := range b.config.BlockLists {
				if entry.Enabled {
					blocklist = append(blocklist, entry.URI)
					list_ids = append(list_ids, i)
				}
			}
			BLmtx.RUnlock()
		}

		for i, url := range blocklist {
			log.Infof("Block list update started %q", url)
			resp, err := http.Get(url)
			if err != nil {
				log.Warningf("Failed to download block list %q: %s", url, err)
				continue
			}
			if err := listRead(resp.Body, b.update, int64(list_ids[i])); err != nil {
				log.Warningf("Failed to parse block list %q: %s", url, err)
			}
			domains += len(b.update)
			resp.Body.Close()

			log.Infof("Block list update finished %q", url)
		}

		log.Infof("Updating database with new domains")

		Dmtx.Lock()
		b.domains = b.update
		Dmtx.Unlock()

		b.update = make(map[string]DomainValue)

		gMetrics.BlockedDomains = int64(domains)
		log.Infof("Block lists updated: %d domains added", domains)
	}()
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
