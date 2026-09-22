package config

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestPath(t *testing.T) {
	t.Run("explicit override wins", func(t *testing.T) {
		want := filepath.Join(t.TempDir(), "elsewhere.yml")
		t.Setenv("DBMAP_CONFIG", want)
		got, err := Path()
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
	})

	t.Run("xdg config home", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("DBMAP_CONFIG", "")
		t.Setenv("XDG_CONFIG_HOME", home)
		got, err := Path()
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(home, "dbmap", Name)
		if got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
	})

	t.Run("platform default", func(t *testing.T) {
		t.Setenv("DBMAP_CONFIG", "")
		t.Setenv("XDG_CONFIG_HOME", "")
		if runtime.GOOS == "windows" {
			t.Setenv("AppData", `C:\Users\example\AppData\Roaming`)
		}
		got, err := Path()
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Base(got) != Name || !filepath.IsAbs(got) {
			t.Errorf("path = %q, want an absolute path ending in %s", got, Name)
		}
	})
}
