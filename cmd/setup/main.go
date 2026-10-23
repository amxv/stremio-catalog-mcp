// Command setup provisions a personal deployment without depending on any
// developer-specific credentials, CLIs, dotfiles or Cloudflare accounts.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type settings struct {
	project     string
	database    string
	region      string
	scope       string
	baseURL     string
	emailFile   string
	apiKeyFile  string
	stremioFile string
	apply       bool
	yes         bool
	copyOnly    bool
	noGit       bool
	noDeploy    bool
}

var simpleName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

func parseSettings(args []string, root string) (settings, error) {
	var cfg settings
	f := flag.NewFlagSet("setup", flag.ContinueOnError)
	f.SetOutput(os.Stderr)
	defaultProject := filepath.Base(root)
	f.StringVar(&cfg.project, "project", defaultProject, "Vercel project name")
	f.StringVar(&cfg.database, "database", "", "dedicated Upstash Redis database name (defaults to PROJECT-redis)")
	f.StringVar(&cfg.region, "region", "us-east-1", "Upstash primary region, usually near Vercel iad1")
	f.StringVar(&cfg.scope, "scope", "", "Vercel team slug; omit for personal accounts")
	f.StringVar(&cfg.baseURL, "base-url", "", "public HTTPS API origin; defaults to https://PROJECT.vercel.app")
	f.StringVar(&cfg.emailFile, "upstash-email-file", "", "private text file containing the Upstash account email")
	f.StringVar(&cfg.apiKeyFile, "upstash-key-file", "", "private text file containing the Upstash Developer API key")
	f.StringVar(&cfg.stremioFile, "stremio-key-file", "", "private text file containing a Stremio account session key")
	f.BoolVar(&cfg.apply, "apply", false, "provision resources and deploy (without this, show a plan)")
	f.BoolVar(&cfg.yes, "yes", false, "agent mode: skip final confirmation after the human authorizes setup")
	f.BoolVar(&cfg.copyOnly, "copy-url", false, "copy the saved authenticated MCP URL to the clipboard")
	f.BoolVar(&cfg.noGit, "no-git-connect", false, "skip connecting GitHub for future deployments")
	f.BoolVar(&cfg.noDeploy, "no-deploy", false, "configure services and secrets but skip the initial production deployment")
	if err := f.Parse(args); err != nil {
		return settings{}, err
	}
	if len(f.Args()) != 0 {
		return settings{}, errors.New("unexpected positional arguments")
	}
	if cfg.copyOnly {
		return cfg, nil
	}
	if !simpleName.MatchString(cfg.project) {
		return settings{}, errors.New("--project must be lowercase letters, digits, dashes; start with a letter and be at most 63 characters")
	}
	if cfg.database == "" {
		cfg.database = cfg.project + "-redis"
	}
	if len(cfg.database) > 63 || !simpleName.MatchString(cfg.database) {
		return settings{}, errors.New("--database must be 1-63 lowercase letters, digits or dashes")
	}
	if cfg.region == "" || !regexp.MustCompile(`^[a-z]{2}-[a-z]+-[0-9]+$`).MatchString(cfg.region) {
		return settings{}, errors.New("invalid Upstash region")
	}
	if cfg.baseURL == "" {
		cfg.baseURL = "https://" + cfg.project + ".vercel.app"
	}
	if err := validateBaseURL(cfg.baseURL); err != nil {
		return settings{}, err
	}
	return cfg, nil
}

func validateBaseURL(base string) error {
	if !strings.HasPrefix(base, "https://") || strings.ContainsAny(strings.TrimPrefix(base, "https://"), "/?# ") || strings.Count(base, ".") < 1 || strings.Contains(base, "@") {
		return errors.New("--base-url must be a single HTTPS origin without path, query, credentials or fragment")
	}
	return nil
}

func printPlan(c settings) {
	fmt.Printf("Self-hosting plan (nothing changed unless --apply is provided):\n")
	fmt.Printf("  Vercel project: %s\n", c.project)
	fmt.Printf("  Public API URL: %s\n", c.baseURL)
	fmt.Printf("  Upstash Redis: %s, FREE, %s, TLS on, eviction off\n", c.database, c.region)
	fmt.Println("  Secrets: distinct MCP/feed tokens, private Redis REST credentials")
	fmt.Println("  Optional: Stremio account session for automatic new-row syncing")
	fmt.Println("  Output: production deployment and secret MCP URL copied to clipboard")
	fmt.Println("  Existing resources: detected and reused; no paid upgrades or secret rotation")
	if !c.apply {
		fmt.Println("Run with --apply to provision after human approval.")
	}
}

func confirm() error {
	fmt.Fprint(os.Stderr, "Provision the Free database and deploy to Vercel? [y/N]: ")
	reader := bufio.NewReader(os.Stdin)
	answer, err := reader.ReadString('\n')
	if err != nil || strings.ToLower(strings.TrimSpace(answer)) != "y" {
		return errors.New("setup not authorized")
	}
	return nil
}

func main() {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cfg, err := parseSettings(os.Args[1:], root)
	if err == nil {
		err = execute(context.Background(), root, cfg)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Setup:", err)
		os.Exit(1)
	}
}

func execute(ctx context.Context, root string, cfg settings) error {
	if cfg.copyOnly {
		keys, found, err := loadKeys(root)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("no local setup keys yet; finish `go run ./cmd/setup --apply` first")
		}
		if err := copyURL(mcpURL(keys)); err != nil {
			return err
		}
		fmt.Println("Authenticated MCP URL copied to clipboard. The secret was not printed.")
		return nil
	}
	printPlan(cfg)
	if !cfg.apply {
		return nil
	}
	if !cfg.yes {
		if err := confirm(); err != nil {
			return err
		}
	}
	return provision(ctx, root, cfg)
}

func provision(ctx context.Context, root string, cfg settings) error {
	return provisionWith(ctx, root, cfg, func(email, apiKey string) upstashClient {
		return upstashClient{email: email, key: apiKey}
	}, copyURL)
}

// The external API client and clipboard adapter are injected so setup can be
// integration-tested without touching a real Vercel account, Upstash DB, or OS
// clipboard. Production always uses the pinned Upstash Developer API origin.
func provisionWith(ctx context.Context, root string, cfg settings, makeUpstash func(string, string) upstashClient, clipboard func(string) error) error {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	keys, found, err := loadKeys(root)
	if err != nil {
		return err
	}
	if found && keys.Project != cfg.project {
		return errors.New("saved connection keys belong to a different project; do not rotate production credentials implicitly")
	}

	fmt.Println("1/5 Checking your Vercel project and existing production variables...")
	vercel := vercelRunner{root: root, scope: cfg.scope}
	project, err := vercel.ensureProject(ctx, cfg.project)
	if err != nil {
		return err
	}
	actualBaseURL, err := vercel.canonicalOrigin(ctx, project, cfg.baseURL)
	if err != nil {
		return err
	}
	cfg.baseURL = actualBaseURL
	if found && keys.BaseURL != cfg.baseURL {
		return errors.New("saved connection URL differs from the requested Vercel domain; do not rotate production credentials implicitly")
	}
	env, err := vercel.productionEnv(ctx, project)
	if err != nil {
		return err
	}
	if !found && (env["MCP_API_KEY"] || env["STREMIO_FEED_TOKEN"] || env["UPSTASH_REDIS_REST_URL"] || env["UPSTASH_REDIS_REST_TOKEN"] || env["PUBLIC_BASE_URL"]) {
		return errors.New("existing project has production configuration but no local key receipt. Refusing to mix credentials or overwrite them; recover the original setup or choose a fresh Vercel project")
	}

	fmt.Println("2/5 Provisioning or reusing your dedicated Free Upstash database...")
	email, err := readPrivateValue(cfg.emailFile, "UPSTASH_EMAIL", "Upstash Developer API account email", false)
	if err != nil {
		return err
	}
	apiKey, err := readPrivateValue(cfg.apiKeyFile, "UPSTASH_API_KEY", "Upstash Developer API key", false)
	if err != nil {
		return err
	}
	database, err := makeUpstash(email, apiKey).database(ctx, cfg.database, cfg.region)
	if err != nil {
		return err
	}
	if found && keys.DatabaseID != database.ID {
		return errors.New("existing connection keys belong to another Upstash database; refusing to rebind secrets")
	}
	restURL, restToken, err := database.credentials()
	if err != nil {
		return err
	}
	if !found {
		keys = connectionKeys{Project: cfg.project, DatabaseID: database.ID, BaseURL: cfg.baseURL}
		keys.MCPKey, err = newToken()
		if err != nil {
			return err
		}
		keys.StremioKey, err = newToken()
		if err != nil {
			return err
		}
		if err := saveKeys(root, keys); err != nil {
			return fmt.Errorf("couldn't securely save the key receipt: %w", err)
		}
	}
	fmt.Println("   Free Redis is ready. Generated keys are saved with owner-only permissions; API credentials remain in memory.")

	fmt.Println("3/5 Configuring protected production variables in Vercel...")
	vars := []struct {
		key    string
		value  string
		secret bool
	}{
		{"MCP_API_KEY", keys.MCPKey, true},
		{"STREMIO_FEED_TOKEN", keys.StremioKey, true},
		{"UPSTASH_REDIS_REST_URL", restURL, true},
		{"UPSTASH_REDIS_REST_TOKEN", restToken, true},
		{"PUBLIC_BASE_URL", cfg.baseURL, false},
	}
	for _, v := range vars {
		if err := vercel.setEnvIfMissing(ctx, env, v.key, v.value, v.secret); err != nil {
			return err
		}
	}
	if !env["STREMIO_AUTH_KEY"] {
		sessionKey, err := readPrivateValue(cfg.stremioFile, "STREMIO_AUTH_KEY", "Stremio session key", true)
		if err != nil {
			return err
		}
		if sessionKey != "" {
			if err := verifyStremioKey(ctx, sessionKey, nil); err != nil {
				return fmt.Errorf("Stremio account key was not verified: %w", err)
			}
			if err := vercel.setEnvIfMissing(ctx, env, "STREMIO_AUTH_KEY", sessionKey, true); err != nil {
				return err
			}
		} else {
			fmt.Println("   Stremio session key not provided. Existing row contents will update, but new rows require manual manifest installation until a session key is configured.")
		}
	}

	fmt.Println("4/5 Setting up automatic Git deployments and publishing the service...")
	if !cfg.noGit {
		if err := vercel.connectGit(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "   Note:", err)
		}
	}
	if !cfg.noDeploy {
		if err := vercel.deploy(ctx); err != nil {
			return err
		}
	}

	fmt.Println("5/5 Copying your authenticated MCP connection URL...")
	if err := clipboard(mcpURL(keys)); err != nil {
		fmt.Fprintln(os.Stderr, "   Clipboard unavailable:", err)
		fmt.Fprintln(os.Stderr, "   An authorized local agent can run `go run ./cmd/setup --copy-url` in a desktop session.")
	} else {
		fmt.Println("   Authenticated MCP URL copied to clipboard (never printed).")
	}
	fmt.Println("Setup complete. Human: ChatGPT → Plugins → Create custom MCP server → No authentication → Name → Add as plugin.")
	if cfg.noDeploy {
		fmt.Println("Note: production deployment was skipped; deploy before adding the MCP plugin.")
	}
	return nil
}
