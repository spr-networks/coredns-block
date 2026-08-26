package block

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func corruptFreelistPages(t *testing.T, filename string) {
	t.Helper()

	db := BoltOpen(filename)
	pageSize := db.Info().PageSize
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	file, err := os.OpenFile(filename, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}

	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		t.Fatal(err)
	}

	flags := make([]byte, 2)
	binary.LittleEndian.PutUint16(flags, 0x01)
	for offset := int64(pageSize*2 + 8); offset < info.Size(); offset += int64(pageSize) {
		if _, err := file.WriteAt(flags, offset); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
	}

	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func verifyRecoveredDatabase(t *testing.T, filename string) {
	t.Helper()

	db := BoltOpen(filename)
	defer db.Close()

	if err := db.View(func(tx *bolt.Tx) error {
		if tx.Bucket([]byte(gDomainBucket)) == nil {
			return ErrBucketMissing
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestBoltOpenRecoversDatabaseFiles(t *testing.T) {
	for _, name := range []string{"dns.db", "dns.db-staging"} {
		t.Run(name+"/invalid", func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), name)
			if err := os.WriteFile(filename, []byte("invalid"), 0664); err != nil {
				t.Fatal(err)
			}
			verifyRecoveredDatabase(t, filename)
		})

		t.Run(name+"/freelist", func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), name)
			corruptFreelistPages(t, filename)
			verifyRecoveredDatabase(t, filename)
		})
	}
}
