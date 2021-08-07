package memory

import (
	"time"

	"github.com/cornelk/hashmap"
)

// Storage interface that is implemented by storage providers
type Storage struct {
	db         *hashmap.HashMap
	gcInterval time.Duration
	done       chan struct{}
}

type entry struct {
	data   []byte
	expiry int64
}

// New creates a new memory storage
func New() *Storage {
	// Create storage
	store := &Storage{
		db:         &hashmap.HashMap{},
		gcInterval: 30 * time.Second,
		done:       make(chan struct{}),
	}

	// Start garbage collector
	go store.gc()

	return store
}

// Get value by key
func (s *Storage) Get(key string) ([]byte, error) {
	if len(key) <= 0 {
		return nil, nil
	}
	v, ok := s.db.Get(key)
	value, ok2 := v.(entry)
	if !ok || !ok2 || value.expiry != 0 && value.expiry <= time.Now().Unix() {
		return nil, nil
	}

	return value.data, nil
}

// Set key with value
func (s *Storage) Set(key string, val []byte, exp time.Duration) error {
	// Ain't Nobody Got Time For That
	if len(key) <= 0 || len(val) <= 0 {
		return nil
	}

	var expire int64
	if exp != 0 {
		expire = time.Now().Add(exp).Unix()
	}

	s.db.Set(key, entry{val, expire})
	return nil
}

// Delete key by key
func (s *Storage) Delete(key string) error {
	// Ain't Nobody Got Time For That
	if len(key) <= 0 {
		return nil
	}
	s.db.Del(key)
	return nil
}

// Reset all keys
func (s *Storage) Reset() error {
	s.db = &hashmap.HashMap{}
	return nil
}

// Close the memory storage
func (s *Storage) Close() error {
	s.done <- struct{}{}
	return nil
}

func (s *Storage) gc() {
	ticker := time.NewTicker(s.gcInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return
		case t := <-ticker.C:
			now := t.Unix()
			for kv := range s.db.Iter() {
				key, ok := kv.Key.(string)
				value, ok2 := kv.Value.(entry)
				if !ok || !ok2 || value.expiry != 0 && value.expiry < now {
					s.db.Del(key)
				}
			}
		}
	}
}
