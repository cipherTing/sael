package store

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
)

func (s *RedisStore) persist(ctx context.Context, b ingestBatch) error {
	s.spoolMu.Lock()
	defer s.spoolMu.Unlock()
	if !s.spooling {
		err := s.publish(ctx, b)
		if err == nil {
			return nil
		}
		slog.Warn("Redis telemetry unavailable; using local spool", "error", err)
	}
	s.spooling = true
	return appendSpool(s.spoolPath+".redis", b)
}

func (s *RedisStore) replay(ctx context.Context) error {
	s.replayMu.Lock()
	defer s.replayMu.Unlock()
	path := s.spoolPath + ".redis.replay"
	s.spoolMu.Lock()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(s.spoolPath+".redis", path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				s.spooling = false
				s.spoolMu.Unlock()
				return nil
			}
			s.spoolMu.Unlock()
			return err
		}
	} else if err != nil {
		s.spoolMu.Unlock()
		return err
	}
	s.spooling = true
	s.spoolMu.Unlock()
	// Replay a rotated file outside the producer lock, so recovery cannot stall new records.
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			break
		} // An incomplete final append was never acknowledged/fsynced.
		if err != nil {
			return err
		}
		var b ingestBatch
		if err := json.Unmarshal(line, &b); err != nil {
			return err
		}
		if err := s.publish(ctx, b); err != nil {
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Remove(path)
}
