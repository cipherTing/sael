package store

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/redis/go-redis/v9"
)

type producerState struct {
	ID      string   `json:"id"`
	Retired []string `json:"retired,omitempty"`
}

// A spool volume owns one stable publisher identity, including across crashes.
func (s *RedisStore) initProducer(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(s.spoolPath), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(s.spoolPath+".producer.lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if err := lockProducerFile(f); err != nil {
		_ = f.Close()
		return errors.New("statistics spool is already owned by another gateway")
	}
	s.producerLock = f
	var state producerState
	raw, err := os.ReadFile(s.spoolPath + ".producer")
	switch {
	case errors.Is(err, os.ErrNotExist):
		state.ID = rand.Text()
	case err != nil:
		return err
	case json.Unmarshal(raw, &state) != nil || state.ID == "":
		return errors.New("invalid statistics producer state")
	}
	s.producer, s.retired = state.ID, map[string]bool{}
	for _, id := range state.Retired {
		s.retired[id] = true
	}
	sequence, err := s.redis.HGet(ctx, ingestProducers, s.producer).Int64()
	if err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	s.producerSequence = sequence
	for _, path := range []string{s.spoolPath + ".redis.replay", s.spoolPath + ".redis"} {
		file, err := os.Open(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		reader := bufio.NewReader(file)
		for {
			line, err := reader.ReadBytes('\n')
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				_ = file.Close()
				return err
			}
			var b ingestBatch
			if err := json.Unmarshal(line, &b); err != nil {
				_ = file.Close()
				return err
			}
			if b.Producer == s.producer {
				s.producerSequence = max(s.producerSequence, b.Sequence)
			} else if b.Producer != "" {
				s.retired[b.Producer] = true
			}
		}
		if err := file.Close(); err != nil {
			return err
		}
	}
	return s.saveProducerState()
}

func (s *RedisStore) saveProducerState() error {
	if s.producerLock == nil {
		return nil
	}
	state := producerState{ID: s.producer}
	for id := range s.retired {
		state.Retired = append(state.Retired, id)
	}
	slices.Sort(state.Retired)
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return writeStateFile(s.spoolPath+".producer", raw)
}

func (s *RedisStore) trackRetiredProducer(id string) error {
	if id == "" || id == s.producer || s.retired[id] {
		return nil
	}
	if s.retired == nil {
		s.retired = map[string]bool{}
	}
	s.retired[id] = true
	return s.saveProducerState()
}

// Called with replay ownership, or after workers stop. Pending files keep their markers.
func (s *RedisStore) cleanupProducerMarkers(ctx context.Context, finished bool) error {
	s.spoolMu.Lock()
	defer s.spoolMu.Unlock()
	for _, path := range []string{s.spoolPath + ".redis", s.spoolPath + ".redis.replay"} {
		if _, err := os.Stat(path); err == nil { // #nosec G703 -- Fixed filenames under the operator-configured spool directory.
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	s.spooling = false
	ids := make([]string, 0, len(s.retired)+1)
	for id := range s.retired {
		ids = append(ids, id)
	}
	if finished && s.producer != "" {
		ids = append(ids, s.producer)
	}
	if len(ids) > 0 {
		if err := s.redis.HDel(ctx, ingestProducers, ids...).Err(); err != nil {
			return err
		}
	}
	clear(s.retired)
	return s.saveProducerState()
}

func writeStateFile(path string, raw []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }() // #nosec G703 -- Remove only the temporary state file created above.
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil { // #nosec G703 -- Atomic state replacement within the operator-configured spool directory.
		return err
	}
	return syncStateDirectory(filepath.Dir(path))
}
