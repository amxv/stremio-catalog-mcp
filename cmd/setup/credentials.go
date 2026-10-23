package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/term"
)

// These are only the two generated application connection keys, not the
// Upstash Developer key, Redis REST token, or Stremio account session key.
type connectionKeys struct {
	Project    string `json:"project"`
	DatabaseID string `json:"database_id"`
	BaseURL    string `json:"base_url"`
	MCPKey     string `json:"mcp_api_key"`
	StremioKey string `json:"stremio_feed_token"`
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func privatePath(root string) string { return filepath.Join(root, ".local-setup", "credentials.json") }

func loadKeys(root string) (connectionKeys, bool, error) {
	path := privatePath(root)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return connectionKeys{}, false, nil
	}
	if err != nil {
		return connectionKeys{}, false, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return connectionKeys{}, false, errors.New("private setup credentials must be a regular file readable only by its owner")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return connectionKeys{}, false, err
	}
	var keys connectionKeys
	if err := json.Unmarshal(data, &keys); err != nil {
		return connectionKeys{}, false, err
	}
	if keys.Project == "" || keys.DatabaseID == "" || keys.BaseURL == "" || len(keys.MCPKey) < 32 || len(keys.StremioKey) < 32 {
		return connectionKeys{}, false, errors.New("private setup credentials are incomplete")
	}
	return keys, true, nil
}

func saveKeys(root string, keys connectionKeys) error {
	directory := filepath.Dir(privatePath(root))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	path := privatePath(root)
	if _, err := os.Lstat(path); err == nil {
		return errors.New("refusing to overwrite saved connection keys")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := json.MarshalIndent(keys, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(directory, ".setup-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func readPrivateValue(fileFlag, envName, label string, optional bool) (string, error) {
	if fileFlag != "" {
		info, err := os.Lstat(fileFlag)
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
			return "", fmt.Errorf("%s file must be a private regular file (chmod 600)", label)
		}
		data, err := os.ReadFile(fileFlag)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}
	if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
		return value, nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		if optional {
			return "", nil
		}
		return "", fmt.Errorf("provide %s using a private --*-file argument or %s", label, envName)
	}
	fmt.Fprintf(os.Stderr, "%s%s: ", label, map[bool]string{true: " (optional; Enter to skip)", false: ""}[optional])
	input, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(input))
	if value == "" && !optional {
		return "", fmt.Errorf("%s is required", label)
	}
	return value, nil
}

func copyURL(value string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "windows":
		cmd = exec.Command("powershell", "-NoProfile", "-Command", "$input | Set-Clipboard")
	default:
		if _, err := exec.LookPath("wl-copy"); err == nil {
			cmd = exec.Command("wl-copy")
		} else if _, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		} else if _, err := exec.LookPath("xsel"); err == nil {
			cmd = exec.Command("xsel", "--clipboard", "--input")
		} else {
			return errors.New("no clipboard utility found (install wl-copy, xclip or xsel)")
		}
	}
	cmd.Stdin = strings.NewReader(value)
	if err := cmd.Run(); err != nil {
		return errors.New("clipboard write failed (try a desktop session or copy from your private credentials file)")
	}
	return nil
}

func mcpURL(keys connectionKeys) string {
	return strings.TrimRight(keys.BaseURL, "/") + "/mcp?key=" + keys.MCPKey
}
