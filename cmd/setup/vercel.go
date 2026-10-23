package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type vercelProject struct {
	ProjectID string `json:"projectId"`
	Project   string `json:"projectName"`
	OrgID     string `json:"orgId"`
}

type vercelRunner struct {
	root  string
	scope string
}

func (v vercelRunner) args(args ...string) []string {
	if v.scope != "" {
		args = append(args, "--scope", v.scope)
	}
	return args
}

func (v vercelRunner) run(ctx context.Context, input io.Reader, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "vercel", v.args(args...)...)
	cmd.Dir = v.root
	cmd.Stdin = input
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		// Keep the output only for preflight failures (no secrets as input).
		return output.Bytes(), fmt.Errorf("vercel %s failed: %w", args[0], err)
	}
	return output.Bytes(), nil
}

func (v vercelRunner) ensureProject(ctx context.Context, name string) (vercelProject, error) {
	if _, err := exec.LookPath("vercel"); err != nil {
		return vercelProject{}, errors.New("Vercel CLI not found; install it and run `vercel login` first")
	}
	output, err := v.run(ctx, nil, "project", "inspect", name)
	if err != nil {
		if !strings.Contains(string(output), "There is no project for") && !strings.Contains(string(output), "not found") {
			return vercelProject{}, fmt.Errorf("cannot inspect Vercel project: %w; check CLI login and --scope", err)
		}
		if _, err := v.run(ctx, nil, "project", "create", name); err != nil {
			return vercelProject{}, fmt.Errorf("could not create Vercel project: %w", err)
		}
	}
	if _, err := v.run(ctx, nil, "link", "--yes", "--project", name); err != nil {
		return vercelProject{}, fmt.Errorf("could not link Vercel project: %w", err)
	}
	data, err := os.ReadFile(filepath.Join(v.root, ".vercel", "project.json"))
	if err != nil {
		return vercelProject{}, err
	}
	var project vercelProject
	if err := json.Unmarshal(data, &project); err != nil {
		return vercelProject{}, err
	}
	if project.Project != name || project.ProjectID == "" || project.OrgID == "" {
		return vercelProject{}, errors.New("linked Vercel project identity doesn't match the requested project")
	}
	return project, nil
}

func (v vercelRunner) productionEnv(ctx context.Context, project vercelProject) (map[string]bool, error) {
	route := fmt.Sprintf("/v10/projects/%s/env?teamId=%s", project.ProjectID, project.OrgID)
	data, err := v.run(ctx, nil, "api", route, "--raw")
	if err != nil {
		return nil, fmt.Errorf("could not list Vercel environment keys: %w", err)
	}
	var result struct {
		Envs []struct {
			Key    string   `json:"key"`
			Target []string `json:"target"`
		} `json:"envs"`
		HiddenProductionEnvCount int `json:"hiddenProductionEnvCount"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, errors.New("Vercel returned an unexpected environment response")
	}
	if result.HiddenProductionEnvCount > 0 {
		return nil, errors.New("Vercel hides some production variables; inspect the project manually before provisioning")
	}
	keys := make(map[string]bool)
	for _, env := range result.Envs {
		for _, target := range env.Target {
			if target == "production" {
				keys[env.Key] = true
			}
		}
	}
	return keys, nil
}

// canonicalOrigin verifies that an advertised API hostname really belongs to
// this Vercel project before it is copied into the user's MCP connection URL.
// A project slug isn't guaranteed to own <slug>.vercel.app globally.
func (v vercelRunner) canonicalOrigin(ctx context.Context, project vercelProject, requested string) (string, error) {
	route := fmt.Sprintf("/v9/projects/%s/domains?teamId=%s", project.ProjectID, project.OrgID)
	data, err := v.run(ctx, nil, "api", route, "--raw")
	if err != nil {
		return "", fmt.Errorf("cannot verify project-owned domains: %w", err)
	}
	var response struct {
		Domains []struct {
			Name     string `json:"name"`
			Verified bool   `json:"verified"`
		} `json:"domains"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return "", errors.New("Vercel returned an unexpected project-domain response")
	}
	u, err := url.Parse(requested)
	if err != nil || u.Scheme != "https" {
		return "", errors.New("invalid requested project origin")
	}
	var verifiedFallback string
	for _, domain := range response.Domains {
		if !domain.Verified {
			continue
		}
		if domain.Name == u.Host {
			return requested, nil
		}
		if strings.HasSuffix(domain.Name, ".vercel.app") {
			verifiedFallback = "https://" + domain.Name
		}
	}
	if requested == "https://"+project.Project+".vercel.app" && verifiedFallback != "" {
		return verifiedFallback, nil
	}
	return "", errors.New("API hostname is not verified on this Vercel project; attach/verify the domain or choose one assigned to the project")
}

func (v vercelRunner) setEnvIfMissing(ctx context.Context, existing map[string]bool, name, value string, secret bool) error {
	if existing[name] {
		return nil
	}
	args := []string{"env", "add", name, "--environment", "production", "--yes"}
	if secret {
		args = append(args, "--sensitive")
	} else {
		args = append(args, "--no-sensitive")
	}
	if _, err := v.run(ctx, strings.NewReader(value), args...); err != nil {
		return fmt.Errorf("failed to configure production variable %s: %w", name, err)
	}
	existing[name] = true
	return nil
}

func (v vercelRunner) connectGit(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", "origin")
	cmd.Dir = v.root
	output, err := cmd.Output()
	if err != nil {
		return errors.New("no Git origin; connect your repository through Vercel's dashboard to enable automatic deployments")
	}
	remote := strings.TrimSpace(string(output))
	if !strings.Contains(remote, "github.com") {
		return errors.New("origin is not a GitHub URL; connect your repository manually in Vercel")
	}
	if _, err := v.run(ctx, nil, "git", "connect", remote, "--yes"); err != nil {
		return errors.New("automatic Git connection failed; connect the repository in Vercel settings")
	}
	return nil
}

func (v vercelRunner) deploy(ctx context.Context) error {
	if _, err := v.run(ctx, nil, "--prod", "--yes"); err != nil {
		return fmt.Errorf("Vercel production deploy failed: %w", err)
	}
	return nil
}
