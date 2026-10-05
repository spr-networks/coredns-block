package block

import (
	"bytes"

	"github.com/blevesearch/vellum"
	bolt "go.etcd.io/bbolt"
)

type blockIndex struct {
	fst  *vellum.FST
	sets []DomainValue
}

func buildIndex(db *bolt.DB) (*blockIndex, error) {
	var buf bytes.Buffer
	builder, err := vellum.New(&buf, nil)
	if err != nil {
		return nil, err
	}

	idx := &blockIndex{}
	seen := map[string]uint64{}

	err = db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(gDomainBucket))
		if bucket == nil {
			return ErrBucketMissing
		}
		return bucket.ForEach(func(k, v []byte) error {
			id, exists := seen[string(v)]
			if !exists {
				item := BucketItem{}
				if item.DecodeValue(v) != nil {
					return nil
				}
				id = uint64(len(idx.sets))
				seen[string(v)] = id
				idx.sets = append(idx.sets, item.Value)
			}
			return builder.Insert(k, id)
		})
	})
	if err != nil {
		return nil, err
	}

	if err = builder.Close(); err != nil {
		return nil, err
	}

	idx.fst, err = vellum.Load(buf.Bytes())
	if err != nil {
		return nil, err
	}
	return idx, nil
}

func (b *Block) loadIndex() error {
	idx, err := buildIndex(b.Db)
	if err != nil {
		return err
	}
	b.index.Store(idx)
	return nil
}
