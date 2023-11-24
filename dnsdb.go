package block

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/nutsdb/nutsdb"
)

var (
	ErrBucketList        = errors.New("error listing buckets")
	ErrBucketGet         = errors.New("error retrieving bucket")
	ErrBucketMissing     = errors.New("bucket doesn't exist")
	ErrBucketCreate      = errors.New("error creating bucket")
	ErrBucketDelete      = errors.New("error deleting bucket")
	ErrBucketDecodeName  = errors.New("error reading bucket name")
	ErrBucketInvalidName = errors.New("invalid bucket name")
	ErrBucketItemDecode  = errors.New("error reading bucket item")
	ErrBucketItemEncode  = errors.New("error encoding bucket item")
	ErrBucketItemGet     = errors.New("error getting bucket item")
	ErrBucketItemCreate  = errors.New("error creating bucket item")
	ErrBucketItemUpdate  = errors.New("error updating bucket item")
	ErrBucketItemDelete  = errors.New("error deleting bucket item")
)

type BucketItem struct {
	Key   string
	Value DomainValue
}

func (item *BucketItem) EncodeKey() []byte {
	return []byte(item.Key)
}

func (item *BucketItem) EncodeValue() ([]byte, error) {

	buf, err := json.Marshal(item.Value)
	if err != nil {
		return nil, ErrBucketItemEncode
	}
	return buf, nil
}

func (item *BucketItem) DecodeValue(rawValue []byte) error {
	if err := json.Unmarshal(rawValue, &item.Value); err != nil {
		return err
	}

	return nil
}

func getItems(db *nutsdb.DB, bucket string) (error, []BucketItem) {
	var items []BucketItem

	err := db.View(func(tx *nutsdb.Tx) error {
		entries, err := tx.GetAll(bucket)
		if err != nil {
			if err == nutsdb.ErrBucketEmpty {
				return nil // Return no error if the bucket is empty
			}
			return err
		}

		for _, entry := range entries {
			bucketItem := BucketItem{Key: string(entry.Key)}
			bucketItem.DecodeValue(entry.Value)
			items = append(items, bucketItem)
		}
		return nil
	})

	return err, items
}
func putItem(db *nutsdb.DB, bucket string, item BucketItem) error {
	itemKey := item.EncodeKey()
	itemValue, err := item.EncodeValue()
	if err != nil {
		return err
	}

	err = db.Update(func(tx *nutsdb.Tx) error {
		return tx.Put(bucket, itemKey, itemValue, 0)
	})

	return err
}
func getCount(db *nutsdb.DB, bucket string) int64 {
	var keyN int64 = 0
	tx, err := db.Begin(false)

	if err != nil {
		fmt.Println(err)
		return keyN
	}

	defer tx.Rollback()

	iterator := nutsdb.NewIterator(tx, gDomainBucket, nutsdb.IteratorOptions{Reverse: false})

	for {
		ok := iterator.Next()

		if !ok {
			return keyN
		}
		keyN++
	}

	return keyN
}

func getItemTx(tx *nutsdb.Tx, bucket string, key string) (error, BucketItem) {
	bucketItem := BucketItem{}
	entry, err := tx.Get(bucket, []byte(key))
	if err != nil {
		return err, bucketItem
	}

	bucketItem.DecodeValue(entry.Value)
	return nil, bucketItem
}

func getItem(db *nutsdb.DB, bucket string, key string) (error, BucketItem) {
	bucketItem := BucketItem{}

	err := db.View(func(tx *nutsdb.Tx) error {
		err, v := getItemTx(tx, bucket, key)
		if err == nil {
			bucketItem = v
		}
		return err
	})

	return err, bucketItem
}

func NutsOpen(filename string) *nutsdb.DB {
	opts := nutsdb.Options{
		Dir:          filename,
		EntryIdxMode: nutsdb.HintKeyAndRAMIdxMode, // Use HintKeyAndRAMIdxMode for smaller file size
		SegmentSize:  16 * 1024 * 1024,            // 16MB instead of the default 256MB
		RWMode: nutsdb.MMap,
		// Other options...
	}

	db, err := nutsdb.Open(opts)
	if err != nil {
		log.Fatal(err)
	}

	return db
}

func storeBatch(db *nutsdb.DB, domains []string, idx int, list_id int) error {
	err := db.Update(func(tx *nutsdb.Tx) error {
		i := 0
		for i < idx {
			domain := domains[i]
			i++

			value := DomainValue{[]int{list_id}, false}
			//see if bucket already has it
			err, item := getItemTx(tx, gDomainBucket, domain)
			if err != nil {
				//add this  current list_id to it.
				value.List_ids = append(item.Value.List_ids, list_id)
			}

			item = BucketItem{domain, value}
			itemValue, err := item.EncodeValue()
			if err == nil {
				err = tx.Put(gDomainBucket, item.EncodeKey(), itemValue, 0)
			}
			if err != nil {
				fmt.Println("putItem failed", domain)
				return err
			}
		}
		return nil
	})
	return err
}

func (b *Block) transferStagingDB() error {
	Stagemtx.Lock()
	defer Stagemtx.Unlock()

	b.Db.Close()

	os.RemoveAll(b.DbPath)

	err := os.Rename(b.DbPath+"-staging", b.DbPath)
	if err != nil {
		return err
	}

	b.Db = NutsOpen(b.DbPath)

	gMetrics.BlockedDomains = getCount(b.Db, gDomainBucket)

	return nil
}

func (b *Block) UpdateDomains(update map[string]DomainValue) error {
	err := b.Db.Update(func(tx *nutsdb.Tx) error {
		for entry, v := range b.update {
			if entry == "" {
				continue
			}
			item := BucketItem{entry, v}
			itemValue, err := item.EncodeValue()
			if err == nil {
				err = tx.Put(gDomainBucket, item.EncodeKey(), itemValue, 0)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		return err
	}

	gMetrics.BlockedDomains = getCount(b.Db, gDomainBucket)

	return nil
}
