package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wilbeibi/catchup/internal/session"
)

// notice runs noticeUpdate with a stubbed tag lookup and reports what reached
// stderr, plus whether the lookup was called at all — a check that never runs
// is the difference between silence and a wasted request.
func notice(t *testing.T, version, tag string, err error, tty bool) (string, bool) {
	t.Helper()
	called := false
	orig := latestTag
	latestTag = func(context.Context) (string, error) {
		called = true
		return tag, err
	}
	t.Cleanup(func() { latestTag = orig })

	var errOut bytes.Buffer
	noticeUpdate(context.Background(), version, tty, &errOut)
	return errOut.String(), called
}

func TestNoticeUpdate(t *testing.T) {
	t.Run("newer release is announced once", func(t *testing.T) {
		got, _ := notice(t, "0.8.0", "v0.9.0", nil, true)
		if !strings.Contains(got, "v0.9.0") {
			t.Errorf("want the new tag on stderr, got %q", got)
		}
		if lines := strings.Count(strings.TrimSpace(got), "\n") + 1; lines != 1 {
			t.Errorf("want one line, got %d:\n%s", lines, got)
		}
	})

	t.Run("current release says nothing", func(t *testing.T) {
		if got, _ := notice(t, "0.9.0", "v0.9.0", nil, true); got != "" {
			t.Errorf("up-to-date build should be silent, got %q", got)
		}
	})

	t.Run("a prerelease build is not told to downgrade", func(t *testing.T) {
		// GitHub's "latest" skips prereleases, so someone running 0.9.0-rc.1
		// sees a v0.8.0 that is genuinely behind them.
		if got, _ := notice(t, "0.9.0-rc.1", "v0.8.0", nil, true); got != "" {
			t.Errorf("want silence ahead of the latest stable, got %q", got)
		}
	})

	t.Run("an older latest says nothing", func(t *testing.T) {
		if got, _ := notice(t, "0.9.0", "v0.8.0", nil, true); got != "" {
			t.Errorf("a build ahead of latest is not behind it, got %q", got)
		}
	})

	t.Run("double-digit minors compare as numbers", func(t *testing.T) {
		if got, _ := notice(t, "0.9.0", "v0.10.0", nil, true); !strings.Contains(got, "v0.10.0") {
			t.Errorf("0.10.0 is newer than 0.9.0, got %q", got)
		}
	})

	t.Run("a go install pseudo-version says nothing", func(t *testing.T) {
		got, _ := notice(t, "0.0.0-20260826031000-918ca6e51f2c", "v0.8.0", nil, true)
		if got != "" {
			t.Errorf("an unversioned build has nothing to compare, got %q", got)
		}
	})

	t.Run("a failed lookup says nothing", func(t *testing.T) {
		if got, _ := notice(t, "0.8.0", "", errors.New("no network"), true); got != "" {
			t.Errorf("offline runs must print exactly what they always did, got %q", got)
		}
	})

	// The guards below must skip the request itself, not just the printing.
	t.Run("piped output is not checked", func(t *testing.T) {
		got, called := notice(t, "0.8.0", "v0.9.0", nil, false)
		if got != "" || called {
			t.Errorf("nobody reads a notice in a pipe: printed %q, called=%v", got, called)
		}
	})

	t.Run("dev builds are not checked", func(t *testing.T) {
		got, called := notice(t, "dev", "v0.9.0", nil, true)
		if got != "" || called {
			t.Errorf("a build from source is not behind: printed %q, called=%v", got, called)
		}
	})

	t.Run("CATCHUP_NO_UPDATE_CHECK is not checked", func(t *testing.T) {
		t.Setenv("CATCHUP_NO_UPDATE_CHECK", "1")
		got, called := notice(t, "0.8.0", "v0.9.0", nil, true)
		if got != "" || called {
			t.Errorf("opt-out must reach the request: printed %q, called=%v", got, called)
		}
	})
}

// --version keeps printing its one parseable line on stdout; the notice is a
// stderr concern, so a script reading `catchup --version` is unaffected.
func TestVersionOutputStaysOnStdout(t *testing.T) {
	orig := latestTag
	latestTag = func(context.Context) (string, error) {
		t.Error("--version into a buffer is not a terminal and must not check")
		return "", nil
	}
	t.Cleanup(func() { latestTag = orig })

	var out, errOut bytes.Buffer
	if err := Run(context.Background(), []string{"--version"}, session.Roots{}, nil, nil, nil, "0.8.0", "", nil, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "catchup 0.8.0\n" {
		t.Errorf("stdout = %q, want the version line alone", got)
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want nothing", errOut.String())
	}
}
