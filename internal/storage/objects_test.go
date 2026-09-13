package storage

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/bmardale/stocat/internal/testutil"
)

func TestLocalObjectStore(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocalStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	testObjectStoreContract(t, store)

	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(t.Context(), "escape/object", bytes.NewReader([]byte("data")), 4); err == nil {
		t.Fatal("write through an escaping symlink succeeded")
	}
	if _, err := os.Stat(filepath.Join(outside, "object")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside object error = %v, want not found", err)
	}
}

func TestS3ObjectStore(t *testing.T) {
	server := testutil.NewRustFS(t)
	config, keys := newTestBucket(t, server, "object-contract")
	config.Prefix = "managed/objects"
	store := NewS3Store(config, keys.AccessKeyID, keys.SecretAccessKey)
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	testObjectStoreContract(t, store)

	content := bytes.Repeat([]byte("m"), multipartPartSize+1)
	object, err := store.Put(t.Context(), "blobs/multipart", bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	if object.Size != int64(len(content)) || object.SHA256 != sha256.Sum256(content) {
		t.Fatalf("multipart object = %+v", object)
	}
}

func testObjectStoreContract(t *testing.T, store ObjectStore) {
	t.Helper()
	ctx := t.Context()
	content := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	wantHash := sha256.Sum256(content)

	object, err := store.Put(ctx, "blobs/01/object", bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	if object.Key != "blobs/01/object" || object.Size != int64(len(content)) || object.SHA256 != wantHash {
		t.Fatalf("object = %+v", object)
	}

	stat, err := store.Stat(ctx, object.Key)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Key != object.Key || stat.Size != object.Size {
		t.Fatalf("stat = %+v, want key %q and size %d", stat, object.Key, object.Size)
	}
	if _, err := store.Put(ctx, object.Key, bytes.NewReader([]byte("different")), 9); !errors.Is(err, ErrExists) {
		t.Fatalf("overwrite error = %v, want exists", err)
	}

	whole, err := store.Open(ctx, object.Key, nil)
	if err != nil {
		t.Fatal(err)
	}
	requireObjectContent(t, whole, content, int64(len(content)))

	part, err := store.Open(ctx, object.Key, &ByteRange{Offset: 10, Length: 8})
	if err != nil {
		t.Fatal(err)
	}
	requireObjectContent(t, part, content[10:18], int64(len(content)))

	if err := store.Delete(ctx, object.Key); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, object.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(ctx, object.Key, nil); !errors.Is(err, ErrMissing) {
		t.Fatalf("missing object error = %v", err)
	}

	for _, key := range []string{"", "/absolute", "../escape", "a/../escape", "a//b"} {
		if _, err := store.Put(ctx, key, bytes.NewReader(nil), 0); !errors.Is(err, ErrConfiguration) {
			t.Errorf("key %q error = %v, want configuration error", key, err)
		}
	}
	if _, err := store.Put(ctx, "blobs/short", bytes.NewReader(content[:2]), 3); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("short body error = %v, want configuration error", err)
	}
	if _, err := store.Put(ctx, "blobs/long", bytes.NewReader(content[:4]), 3); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("long body error = %v, want configuration error", err)
	}
}

func requireObjectContent(t *testing.T, object ReadObject, want []byte, objectSize int64) {
	t.Helper()
	defer func() {
		if err := object.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	got, err := io.ReadAll(object.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) || object.Size != int64(len(want)) || object.ObjectSize != objectSize {
		t.Fatalf("read = %q, size %d/%d", got, object.Size, object.ObjectSize)
	}
}
