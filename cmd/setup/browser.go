package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Agent Browser executes this expression inside the already-authenticated
// Stremio origin. Its JSON output stays in Go process memory and never reaches
// the agent's tool transcript, stdout, the clipboard, or the local receipt.
const stremioSessionExpression = `(function () {
  if (location.origin !== 'https://web.stremio.com') return null;
  try {
    const profile = JSON.parse(localStorage.getItem('profile') || '{}');
    const key = profile?.auth?.key;
    return typeof key === 'string' && key.length >= 20 ? key : null;
  } catch (_) { return null; }
})()`

func browserCommand(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "agent-browser", args...)
	output, err := cmd.Output()
	if err != nil {
		// Deliberately omit stdout/stderr: they can include a session key.
		return nil, errors.New("Agent Browser command failed (verify it is installed and the selected session is available)")
	}
	if len(output) > 128<<10 {
		return nil, errors.New("Agent Browser response is unexpectedly large")
	}
	return output, nil
}

func browserSessionKey(ctx context.Context, session string) (string, error) {
	output, err := browserCommand(ctx, "--session", session, "--json", "eval", stremioSessionExpression)
	if err != nil {
		return "", err
	}
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Origin string  `json:"origin"`
			Result *string `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(output, &response); err != nil || !response.Success {
		return "", errors.New("Agent Browser could not inspect the Stremio login session")
	}
	if response.Data.Origin != "https://web.stremio.com/" && response.Data.Origin != "https://web.stremio.com" {
		return "", errors.New("Agent Browser is not on web.stremio.com; refusing to read another website's session")
	}
	if response.Data.Result == nil || len(*response.Data.Result) < 20 {
		return "", nil
	}
	return *response.Data.Result, nil
}

func sessionViaAgentBrowser(ctx context.Context, opts settings) (string, error) {
	if _, err := exec.LookPath("agent-browser"); err != nil {
		return "", errors.New("Agent Browser CLI not installed; install it or supply a private --stremio-key-file instead")
	}
	session := opts.stremioBrowserSession
	owned := false
	if session == "" {
		id, err := newToken()
		if err != nil {
			return "", err
		}
		session = "stremio-setup-" + id[:12]
		owned = true
		args := []string{"--session", session, "--headed"}
		if runtime.GOOS == "darwin" {
			const chromePath = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
			if _, err := os.Stat(chromePath); err == nil {
				args = append(args, "--executable-path", chromePath)
			}
		}
		args = append(args, "open", "https://web.stremio.com/")
		if _, err := browserCommand(ctx, args...); err != nil {
			return "", errors.New("cannot open headed Stremio browser; try --stremio-key-file or use an existing headed session")
		}
	}
	if owned {
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = browserCommand(closeCtx, "--session", session, "close")
		}()
	}
	fmt.Println("   Stremio Web is ready in Agent Browser. Human: sign in there; the agent will detect the session securely.")
	check := time.NewTicker(3 * time.Second)
	defer check.Stop()
	for {
		key, err := browserSessionKey(ctx, session)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(key) != "" {
			fmt.Println("   Stremio session found in signed-in browser (not displayed or saved locally).")
			return key, nil
		}
		select {
		case <-ctx.Done():
			return "", errors.New("Stremio sign-in was not completed; retry or provide a session key via a private file")
		case <-check.C:
		}
	}
}
