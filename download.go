package block

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
	"fmt"
	"os"

	bolt "go.etcd.io/bbolt"
)

// our default block lists.
var blocklists = []string{"https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts"}

var DLmtx sync.RWMutex

func (b *Block) transferStagingDB() error {
	Stagemtx.Lock()
	defer Stagemtx.Unlock()

	b.Db.Close()

	err := os.Rename(b.DbPath+"-staging", b.DbPath)
	if err != nil {
		return err
	}

	b.Db = BoltOpen(b.DbPath)

	gMetrics.BlockedDomains = getCount(b.Db, gDomainBucket)

//	b.compcatDb()
	return nil
}
func (b *Block) dbStagingDownload(db *bolt.DB, url string, list_id int) error {
	var err error

	Stagemtx.Lock()
	defer Stagemtx.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		fmt.Println("BYE")
		// handle error
		return err
	}
	req = req.WithContext(ctx)

	client := &http.Client{}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		// handle error
		fmt.Println("BYE")
		return err
	}

	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	done := make(chan bool)

	go func() {
		err = db.Update(func(tx *bolt.Tx) error {
			bucket := tx.Bucket([]byte(gDomainBucket))
			for scanner.Scan() {
				// process each line
				ok, domain := lineRead(scanner.Text())
				if ok {
					//see if staging already has it
					value := DomainValue{[]int{list_id}, false}
					/*
					err, item := getItemBucket(bucket, domain)
					if err != nil {
						//add this  current list_id to it.
						value.List_ids = append(item.Value.List_ids, list_id)
					}
					*/
					item := BucketItem{domain, value}
					itemValue, err := item.EncodeValue()
					if err == nil {
						err = bucket.Put(item.EncodeKey(), itemValue)
					}
					if err != nil {
						fmt.Println("putItem failed", domain)
						done <- true
					}
				}
			}
			return nil
		})
		done <- true
	}()

	select {
	case <-done:
		// reading finished
		return err
	case <-ctx.Done():
		// timeout
		fmt.Println("context cancelled, reason:", ctx.Err())
		return errors.New("processing list timed out for " + url)
	}

	return err
}
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

		memEfficient := true

		var db *bolt.DB

		if memEfficient {
			db = BoltOpen(b.DbPath + "-staging")
		}

		for i, url := range blocklists {
			log.Infof("Block list update started %q", url)

			if memEfficient {
				//mem efficient download
				b.dbStagingDownload(db, url, list_ids[i])
			} else {
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
		}

		log.Infof("Updating database with new domains")

		if memEfficient {
			Dmtx.Lock()
			db.Close()
			b.transferStagingDB()
			Dmtx.Unlock()
		} else {
			Dmtx.Lock()
			b.UpdateDomains(b.update)
			Dmtx.Unlock()
			b.update = make(map[string]DomainValue)
		}

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
