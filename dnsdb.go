package block

import (
	"encoding/json"
	"errors"
	"os"
	"time"

	bolt "go.etcd.io/bbolt"
)

//import "fmt"

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

func getItems(db *bolt.DB, bucket string) (error, []BucketItem) {
	items := []BucketItem{}

	err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return ErrBucketMissing
		}
		b.ForEach(func(k, v []byte) error {
			bucketItem := BucketItem{Key: string(k)}
			bucketItem.DecodeValue(v)
			items = append(items, bucketItem)
			return nil
		})
		return nil
	})

	return err, items
}

func putItem(db *bolt.DB, bucket string, item BucketItem) error {
	itemKey := item.EncodeKey()
	itemValue, err := item.EncodeValue()
	if err != nil {
		return err
	}

	err = db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		err = b.Put(itemKey, itemValue)
		return err
	})

	return err
}

func cleanBucket(db *bolt.DB, bucket string) error {

	err := db.Update(func(tx *bolt.Tx) error {
		return tx.DeleteBucket([]byte(bucket))
	})

	return err
}

func (b *Block) compcatDb() error {

	dst, err := bolt.Open(gDbPath+".tmp", 0664, nil)
	defer dst.Close()

	if err != nil {
		return err
	}

	err = bolt.Compact(dst, b.Db, 0)
	if err != nil {
		return err
	}

	err = os.Rename(gDbPath+".tmp", gDbPath)
	if err != nil {
		return err
	}

	//close and re-open db
	b.Db.Close()

	options := &bolt.Options{Timeout: 1 * time.Second}
	db, err := bolt.Open(gDbPath, 0664, options)
	if err != nil {
		log.Fatal("Failed to open", gDbPath, err)
	}
	b.Db = db
	return nil
}

func getCount(db *bolt.DB, bucket string) int64 {
	keyN := int64(0)
	db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b != nil {
			stats := b.Stats()
			keyN = int64(stats.KeyN)
		}
		return nil
	})
	return keyN
}

func getItem(db *bolt.DB, bucket string, key string) (error, BucketItem) {
	bucketItem := BucketItem{}

	err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return ErrBucketMissing
		}
		v := b.Get([]byte(key))
		if v == nil {
			return ErrBucketItemGet
		}
		return bucketItem.DecodeValue(v)
	})

	return err, bucketItem
}
