package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/branow/dbmap/internal/catalog"
)

// The cache is the user's private working copy of their own schema.
const (
	dirPerm  fs.FileMode = 0o700
	filePerm fs.FileMode = 0o600
)

// tempPattern is dotted and unsuffixed, so a half-written file is never
// mistaken for an entry.
const tempPattern = ".partial-*"

// envelope wraps every payload with what a read checks before trusting it.
type envelope struct {
	Artifact string          `json:"artifact"`
	Key      string          `json:"key,omitempty"`
	Signal   catalog.Signal  `json:"signal,omitempty"`
	Checksum string          `json:"checksum"`
	Payload  json.RawMessage `json:"payload"`
}

// read loads one entry. Absent, unparsable and failing checksum all report a
// miss with no error.
func read(path string) (envelope, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return envelope{}, false, nil
	}
	if err != nil {
		return envelope{}, false, &StoreError{Op: OpRead, Path: path, Err: err}
	}
	var entry envelope
	if json.Unmarshal(data, &entry) != nil {
		return envelope{}, false, nil
	}
	if entry.Checksum == "" || entry.Checksum != payloadSum(entry.Payload) {
		return envelope{}, false, nil
	}
	return entry, true, nil
}

// write stores one entry atomically: a flushed temp file renamed over the
// target, so a reader sees the old entry or the new one, never a prefix.
func write(path string, entry envelope) error {
	fail := func(op Op, at string, err error) error {
		return &StoreError{Op: op, Artifact: entry.Artifact, Key: entry.Key, Path: at, Err: err}
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fail(OpWrite, dir, err)
	}
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fail(OpWrite, path, err)
	}

	file, err := os.CreateTemp(dir, tempPattern)
	if err != nil {
		return fail(OpWrite, dir, err)
	}
	temp := file.Name()
	defer os.Remove(temp)

	if err := writeAll(file, append(data, '\n')); err != nil {
		return fail(OpWrite, temp, err)
	}
	if err := os.Chmod(temp, filePerm); err != nil {
		return fail(OpWrite, temp, err)
	}
	if err := os.Rename(temp, path); err != nil {
		return fail(OpWrite, path, err)
	}
	return nil
}

// writeAll flushes before closing, so the rename cannot publish an entry still
// sitting in a buffer.
func writeAll(file *os.File, data []byte) error {
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

// payloadSum digests normalised JSON, never the on-disk bytes: entries are
// pretty-printed on the way out, so hashing as written made every read look
// corrupt and the cache never hit.
func payloadSum(payload []byte) string {
	var compact bytes.Buffer
	if json.Compact(&compact, payload) != nil {
		return checksum(payload)
	}
	return checksum(compact.Bytes())
}

func checksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
