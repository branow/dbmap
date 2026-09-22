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

// The cache is the user's private working copy of their own schema. It holds no
// credential, but it is nobody else's business either.
const (
	dirPerm  fs.FileMode = 0o700
	filePerm fs.FileMode = 0o600
)

// tempPattern names the file an interrupted write leaves behind. It starts with
// a dot and carries no .json suffix, so a crashed write can never be mistaken
// for an entry: entries are read by their exact computed path and nothing else.
const tempPattern = ".partial-*"

// envelope wraps every payload with what a read must check before it trusts the
// bytes: which artifact wrote them, which object and which modify signal they
// describe, and a checksum over the payload. A file failing any check is a
// miss, never a value — that is the whole defence against deserialising garbage
// left by a killed run.
type envelope struct {
	Artifact string          `json:"artifact"`
	Key      string          `json:"key,omitempty"`
	Signal   catalog.Signal  `json:"signal,omitempty"`
	Checksum string          `json:"checksum"`
	Payload  json.RawMessage `json:"payload"`
}

// read loads one entry. A file that is absent, unparsable, or whose payload
// does not match its checksum reports a miss with no error: all three mean the
// same thing to a caller, which is that the value must be fetched again.
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

// write stores one entry atomically: a fully written and flushed temporary file
// is renamed over the target, so a reader sees either the previous entry or the
// new one and never a prefix of either. A killed write leaves only the
// temporary file, which no read path will ever open.
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

// writeAll puts the bytes on disk and closes the file, flushing before it does,
// so the rename that follows cannot publish an entry whose contents are still
// in a buffer.
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

// payloadSum digests a payload independently of how it is laid out on disk.
// An entry is pretty-printed on the way out, which re-indents the payload
// embedded in it, so digesting the bytes exactly as they appear would compare an
// indented payload against the compact one the write digested and condemn every
// entry as corrupt. Normalising first is what makes the checksum mean "these
// bytes were altered" rather than "these bytes were reformatted".
func payloadSum(payload []byte) string {
	var compact bytes.Buffer
	if json.Compact(&compact, payload) != nil {
		return checksum(payload)
	}
	return checksum(compact.Bytes())
}

// checksum is the digest over exactly the bytes given.
func checksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
