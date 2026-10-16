package zone

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// EnablePersistence loads an existing record file or seeds a missing file with
// this zone's records. Invalid files are never overwritten. It must be enabled
// once before serving; only one process may own a given file. Successful startup
// advances the persisted revision, invalidating forms from an earlier process.
func (z *Zone) EnablePersistence(path string) error {
	if path == "" {
		return fmt.Errorf("record file path must not be empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.origin == "" {
		return fmt.Errorf("uninitialized zone")
	}
	if z.stateFile != "" {
		return fmt.Errorf("record persistence is already enabled")
	}
	replacement := z
	version := z.revision
	file, err := os.Open(abs)
	if err == nil {
		data, readErr := io.ReadAll(io.LimitReader(file, maxRecordFileBytes+1))
		closeErr := file.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if len(data) > maxRecordFileBytes {
			return fmt.Errorf("record file exceeds 16 MiB")
		}
		replacement, version, err = decodeRecordFile(data, z.origin)
		if err != nil {
			return fmt.Errorf("load records %q: %w", abs, err)
		}
		version++
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("open records %q: %w", abs, err)
	} else {
		// The seed has not been published yet. Build a separate candidate so
		// encodeRecordFile can read it without recursively taking z.mu.
		var records = z.recordList()
		replacement, err = NewZone(z.origin, records)
		if err != nil {
			return err
		}
	}
	if err := writeRecordFile(abs, replacement, version); err != nil {
		return err
	}
	z.records, z.names, z.soa = replacement.records, replacement.names, replacement.soa
	z.revision, z.stateFile = version, abs
	return nil
}

// Persistent reports whether record mutations are backed by a file.
func (z *Zone) Persistent() bool {
	z.mu.RLock()
	defer z.mu.RUnlock()
	return z.stateFile != ""
}

// commit requires z.mu's write lock. Rename is the persistence commit point;
// nothing that can return an error runs after rename before publishing memory.
func (z *Zone) commit(replacement *Zone) error {
	if z.revision == ^uint64(0) {
		return fmt.Errorf("record revision is exhausted")
	}
	next := z.revision + 1
	if z.stateFile != "" {
		if err := writeRecordFile(z.stateFile, replacement, next); err != nil {
			return err
		}
	}
	z.records, z.names, z.soa = replacement.records, replacement.names, replacement.soa
	z.revision = next
	return nil
}

func writeRecordFile(path string, z *Zone, revision uint64) error {
	data, err := encodeRecordFile(z, revision)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create record directory: %w", err)
	}
	parent, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer parent.Close()
	file, err := os.CreateTemp(dir, ".lantern-records-*")
	if err != nil {
		return fmt.Errorf("create record file: %w", err)
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return fmt.Errorf("write records: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync records: %w", err)
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return fmt.Errorf("replace records: %w", err)
	}
	// The rename has committed. Directory sync improves power-loss durability
	// where supported, but failure cannot be reported as a rejected edit after
	// the file has changed. File contents were synced before the rename.
	_ = parent.Sync()
	return nil
}
