package credentials

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// FileName is the plaintext store's file. It is never committed: the
// repository's ignore rules deny it by name.
const FileName = "credentials.yml"

// File stores secrets in a 0600 file next to the config. It exists only for
// machines with no usable keychain, and it is reached only when the policy
// explicitly opts in: nothing falls back to it silently.
type File struct{ Path string }

// NewFile returns a plaintext store at a path.
func NewFile(path string) *File { return &File{Path: path} }

// FilePath reports the default plaintext store location, beside config.yml.
func FilePath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), FileName)
}

func (f *File) Get(key string) (Secret, error) {
	entries, err := f.read()
	if err != nil {
		return Secret{}, err
	}
	value, ok := entries[key]
	if !ok {
		return Secret{}, &NotFoundError{Key: key}
	}
	return NewSecret(value), nil
}

func (f *File) Set(key string, secret Secret) error {
	entries, err := f.read()
	if err != nil {
		return err
	}
	entries[key] = secret.Reveal()
	return f.write(entries)
}

func (f *File) Delete(key string) error {
	entries, err := f.read()
	if err != nil {
		return err
	}
	if _, ok := entries[key]; !ok {
		return nil
	}
	delete(entries, key)
	return f.write(entries)
}

func (f *File) read() (map[string]string, error) {
	raw, err := os.ReadFile(f.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	entries := map[string]string{}
	if err := yaml.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func (f *File) write(entries map[string]string) error {
	raw, err := yaml.Marshal(entries)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(f.Path, raw, 0o600)
}
