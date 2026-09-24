package connect

import (
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/branow/dbmap/internal/config"
)

// CredCacheParam overrides the credential cache this connection reads. It is an
// escape hatch: resolution below finds the right cache on its own.
const CredCacheParam = "krb5-credcachefile"

// RealmParam names the Kerberos realm.
const RealmParam = "krb5-realm"

// ccachePrefix matches a cache type prefix. Two or more letters, so a Windows
// drive letter is not mistaken for one.
var ccachePrefix = regexp.MustCompile(`^([A-Za-z]{2,}):(.*)$`)

// snapshotters copy a cache type the pure-Go driver cannot read into one it can.
// macOS Heimdal ships kcc, and its API: cache is the case this exists for; MIT
// has no equivalent for KEYRING:/KCM:, which fall through to a typed error
// naming the manual step.
var snapshotters = []struct {
	Name string
	Args func(target string) []string
}{
	{Name: "kcc", Args: func(target string) []string {
		return []string{"copy_cred_cache", "FILE:" + target}
	}},
}

// credentialCache returns a FILE: cache the driver can read.
//
// The driver reads FILE: caches only, while macOS defaults to a Keychain-backed
// API: cache. Rather than making that the user's problem, an unreadable type is
// snapshotted into a private file under the user's cache directory. The
// snapshot is taken per connect, so it cannot go stale behind a ticket refresh.
func credentialCache(cfg config.Connection, env environment) (string, error) {
	if path := strings.TrimSpace(cfg.Params[CredCacheParam]); path != "" {
		return readable(strings.TrimPrefix(path, "FILE:"), env)
	}

	name := strings.TrimSpace(env.getenv("KRB5CCNAME"))
	if name == "" {
		// Nothing names a cache, so the platform default applies. On macOS that
		// is an API: cache rather than a file, and it is named nowhere — so try
		// the file default first and convert whatever the tools use otherwise.
		if path, err := readable(defaultCache(env), env); err == nil {
			return path, nil
		}
		return snapshot("", "", env)
	}

	match := ccachePrefix.FindStringSubmatch(name)
	if match == nil {
		return readable(name, env)
	}
	kind, path := strings.ToUpper(match[1]), match[2]
	if kind == "FILE" {
		return readable(path, env)
	}
	return snapshot(name, kind, env)
}

// snapshot copies the ambient cache into one the driver can read. A failure
// reports the type it could not convert, so the message names the real problem
// rather than claiming there is no ticket.
func snapshot(source, kind string, env environment) (string, error) {
	target, err := snapshotPath(env)
	if err != nil {
		return "", &CredentialCacheError{Type: kind, Path: defaultCache(env), Err: err}
	}
	if err := env.mkdirAll(filepath.Dir(target), 0o700); err != nil {
		return "", &CredentialCacheError{Type: kind, Path: target, Err: err}
	}

	for _, tool := range snapshotters {
		path, err := env.look(tool.Name)
		if err != nil {
			continue
		}
		var extra []string
		if source != "" {
			extra = append(extra, "KRB5CCNAME="+source)
		}
		if err := env.run(path, tool.Args(target), extra...); err != nil {
			// A conversion that fails with no ticket to convert is a missing
			// ticket, and asking for kinit is the useful answer.
			return "", &CredentialCacheError{Type: kind, Path: defaultCache(env), Err: err}
		}
		// The snapshot carries live tickets, so it is no more readable than the
		// cache it came from.
		if err := env.chmod(target, 0o600); err != nil {
			return "", &CredentialCacheError{Type: kind, Path: target, Err: err}
		}
		return readable(target, env)
	}
	if kind == "" {
		// Nothing named a cache and no file default exists, so there is no
		// ticket to convert rather than a type we cannot handle.
		return "", &CredentialCacheError{Path: defaultCache(env), Err: fs.ErrNotExist}
	}
	return "", &CredentialCacheError{Type: kind, Path: defaultCache(env), Unconvertible: true}
}

// snapshotPath is where the converted cache lives: the user's cache directory,
// not the index tree, because it holds live credentials.
func snapshotPath(env environment) (string, error) {
	dir, err := env.cacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "dbmap", "krb5cc"), nil
}

// MITCacheDir is where the MIT default credential cache lives. Hard-coded
// rather than taken from os.TempDir, which honours TMPDIR - and on macOS that
// is a per-user directory under /var/folders, so the "default" this names would
// be a path no Kerberos tool has ever written to, in a remedy line the user is
// being asked to act on.
const MITCacheDir = "/tmp"

// defaultCache is the MIT default, and what a remedy names.
func defaultCache(env environment) string {
	return filepath.Join(MITCacheDir, "krb5cc_"+env.uid)
}

// readable refuses a cache that is missing or unopenable, so an absent ticket
// and an unusable one get the same remedy.
func readable(path string, env environment) (string, error) {
	info, err := env.stat(path)
	if err != nil {
		return "", &CredentialCacheError{Path: path, Err: err}
	}
	if info.IsDir() {
		return "", &CredentialCacheError{Type: "DIR", Path: path}
	}
	return path, nil
}

// environment is the ambient state cache resolution reads, as a struct so a
// test needs no env var, no file on disk and no subprocess.
type environment struct {
	getenv   func(string) string
	stat     func(string) (os.FileInfo, error)
	cacheDir func() (string, error)
	look     func(string) (string, error)
	run      func(name string, args []string, env ...string) error
	mkdirAll func(string, os.FileMode) error
	chmod    func(string, os.FileMode) error
	uid      string
}

func ambient() environment {
	uid := ""
	if current, err := user.Current(); err == nil {
		uid = current.Uid
	}
	return environment{
		getenv:   os.Getenv,
		stat:     os.Stat,
		cacheDir: os.UserCacheDir,
		look:     exec.LookPath,
		run:      runTool,
		mkdirAll: os.MkdirAll,
		chmod:    os.Chmod,
		uid:      uid,
	}
}

func runTool(name string, args []string, extra ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), extra...)
	return cmd.Run()
}
