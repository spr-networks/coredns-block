package block

import (
	"net/http"
	"sync"
	"time"
)

// our default block lists.
var blocklists = []string{"https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts"}

var DLmtx sync.Mutex

func (b *Block) download() {

	go func() {
		DLmtx.Lock()
		defer DLmtx.Unlock()

		list_ids := []int{}
		if b.superapi_enabled {
			//override blocklist with config
			blocklists = []string{}
			BLmtx.RLock()
			for i, entry := range b.config.BlockLists {
				if entry.Enabled {
					blocklists = append(blocklists, entry.URI)
					list_ids = append(list_ids, i)
				}
			}
			BLmtx.RUnlock()
		}

		for i, url := range blocklists {
			log.Infof("Block list update started %q", url)
			resp, err := http.Get(url)
			if err != nil {
				log.Warningf("Failed to download block list %q: %s", url, err)
				continue
			}
			if err := listRead(resp.Body, b.update, int(list_ids[i])); err != nil {
				log.Warningf("Failed to parse block list %q: %s", url, err)
			}
			resp.Body.Close()

			log.Infof("Block list update finished %q %d", url, len(b.update))
		}

		log.Infof("Updating database with new domains")

		Dmtx.Lock()
		b.UpdateDomains(b.update)
		Dmtx.Unlock()

		b.update = make(map[string]DomainValue)
		log.Infof("Block lists updated: %d domains added", gMetrics.BlockedDomains)
	}()
}

func (b *Block) refresh() {
	refreshTime := time.Hour * 24 * 7
	if b.config.RefreshSeconds > 0 {
		refreshTime = time.Duration(b.config.RefreshSeconds) * time.Second
	}
	tick := time.NewTicker(refreshTime)
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
