package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// WithCache serves repeated identical requests from disk, so a killed run
// resumes free and tests are deterministic without a fake.
//
// Entries are content-addressed by sha256(provider|model|system|prompt|schema),
// so a changed prompt simply misses and no invalidation is needed. Only the
// response is stored: a prompt may carry sampled database rows, and those must
// not be written to a cache directory.
//
// A hit replays the recorded [Usage] as well. Place [WithUsage] inside this
// wrapper, not outside it, when the totals are meant to count money actually
// spent.
//
// A cache that cannot be read or written is not an error: the call falls
// through to the provider.
func WithCache(next Client, dir string) Client {
	if dir == "" {
		return next
	}
	return &cache{next: next, dir: dir}
}

type cache struct {
	next Client
	dir  string
}

func (c *cache) Name() string { return c.next.Name() }

func (c *cache) Complete(ctx context.Context, req Request) (*Response, error) {
	path := c.path(req)
	if resp, ok := read(path); ok {
		return resp, nil
	}
	resp, err := c.next.Complete(ctx, req)
	if err != nil {
		return nil, err
	}
	write(path, resp)
	return resp, nil
}

// CacheKey is the content address of a request under a given provider: the
// hex sha256 of provider, model, system, prompt and schema, separated by a byte
// that cannot occur inside any of them.
func CacheKey(provider string, req Request) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		provider,
		req.Model,
		req.System,
		req.Prompt,
		string(req.Schema),
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

func (c *cache) path(req Request) string {
	key := CacheKey(c.Name(), req)
	return filepath.Join(c.dir, key[:2], key[2:]+".json")
}

func read(path string) (*Response, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var resp Response
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, false
	}
	return &resp, true
}

func write(path string, resp *Response) {
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return
	}
	name := temp.Name()
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		os.Remove(name)
		return
	}
	if err := temp.Close(); err != nil {
		os.Remove(name)
		return
	}
	if err := os.Rename(name, path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		os.Remove(name)
	}
}
