package store

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
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
		if err := removeIfExists(path + ".offset"); err != nil {
			s.spoolMu.Unlock()
			return err
		}
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
	offset, err := replayOffset(path, f)
	if err != nil {
		return err
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	reader := bufio.NewReader(f)
	// Leave time for database consumption between bounded replay passes.
	for i := 0; i < 128; i++ {
		if err := ctx.Err(); err != nil {
			return saveReplayProgress(path, offset, err)
		}
		line, err := reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			if err := f.Close(); err != nil {
				return err
			}
			if err := os.Remove(path); err != nil {
				return err
			}
			if err := removeIfExists(path + ".offset"); err != nil {
				return err
			}
			return s.cleanupProducerMarkers(ctx, false)
		} // An incomplete final append was never acknowledged/fsynced.
		if err != nil {
			return saveReplayProgress(path, offset, err)
		}
		var b ingestBatch
		if err := json.Unmarshal(line, &b); err != nil {
			return saveReplayProgress(path, offset, err)
		}
		if err := s.trackRetiredProducer(b.Producer); err != nil {
			return err
		}
		if err := s.publish(ctx, b); err != nil {
			return saveReplayProgress(path, offset, err)
		}
		offset += int64(len(line))
	}
	return saveReplayProgress(path, offset, nil)
}

func replayOffset(path string, f *os.File) (int64, error) {
	raw, err := os.ReadFile(path + ".offset")
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	offset, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil || offset < 0 {
		return 0, errors.New("invalid Redis spool offset")
	}
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	if offset > info.Size() {
		return 0, errors.New("redis spool offset exceeds file length")
	}
	if offset > 0 {
		var last [1]byte
		if _, err := f.ReadAt(last[:], offset-1); err != nil || last[0] != '\n' {
			return 0, errors.New("redis spool offset is not a record boundary")
		}
	}
	return offset, nil
}

func saveReplayProgress(path string, offset int64, cause error) error {
	if err := writeStateFile(path+".offset", []byte(strconv.FormatInt(offset, 10)+"\n")); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func removeIfExists(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
