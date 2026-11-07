package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nod/ntx/internal/auth"
	"github.com/nod/ntx/internal/config"
	"github.com/nod/ntx/internal/db"
	"github.com/nod/ntx/internal/handlers"
	"github.com/nod/ntx/internal/models"
	"github.com/nod/ntx/internal/people"
	"github.com/nod/ntx/internal/server"
	"github.com/nod/ntx/internal/sitereg"
	"github.com/nod/ntx/internal/sso"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var version = "dev"

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "ntx",
		Short: "personal site server",
	}

	serve := &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP server",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgFile, _ := cmd.Flags().GetString("config")
			a, err := openApp(cfgFile)
			if err != nil {
				return err
			}
			defer a.close()

			srv := server.New(a.cfg, a.handler, a.registry, a.auth)

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			go func() {
				log.Printf("listening on %s (sign in as root on %s)", srv.Addr, a.cfg.PlatformHostname)
				if err := srv.ListenAndServe(); err != nil {
					log.Printf("server stopped: %v", err)
				}
			}()

			<-ctx.Done()
			log.Printf("shutting down...")
			shutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			return srv.Shutdown(shutCtx)
		},
	}
	serve.Flags().String("config", "", "config file (default: ntx.toml)")

	generate := &cobra.Command{
		Use:   "generate",
		Short: "Generate a static site for every site in the database",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgFile, _ := cmd.Flags().GetString("config")
			outDir, _ := cmd.Flags().GetString("out")
			a, err := openApp(cfgFile)
			if err != nil {
				return err
			}
			defer a.close()
			return a.handler.Generate(outDir)
		},
	}
	generate.Flags().String("config", "", "config file (default: ntx.toml)")
	generate.Flags().String("out", "./out", "output directory")

	hashPw := &cobra.Command{
		Use:   "hash-password",
		Short: "Bcrypt a password for ntx.toml's root_password_hash",
		RunE: func(cmd *cobra.Command, args []string) error {
			var pw string
			if term.IsTerminal(int(os.Stdin.Fd())) {
				fmt.Fprint(os.Stderr, "Password: ")
				b, err := term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Fprintln(os.Stderr)
				if err != nil {
					return err
				}
				pw = string(b)
			} else {
				var err error
				pw, err = readPasswordLine(os.Stdin)
				if err != nil {
					return err
				}
			}
			hash, err := hashRootPassword(pw)
			if err != nil {
				return err
			}
			fmt.Println(hash)
			return nil
		},
	}

	genConfig := &cobra.Command{
		Use:   "gen-config",
		Short: "Print a sample ntx.toml to stdout",
		RunE: func(cmd *cobra.Command, args []string) error {
			promptPw, _ := cmd.Flags().GetBool("password")

			sessionSecret, err := randomSecret()
			if err != nil {
				return err
			}
			encryptionKey, err := randomSecret()
			if err != nil {
				return err
			}

			hash := ""
			if promptPw {
				fmt.Fprint(os.Stderr, "Root password: ")
				pw, err := term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Fprintln(os.Stderr)
				if err != nil {
					return err
				}
				hash, err = hashRootPassword(string(pw))
				if err != nil {
					return err
				}
			}

			fmt.Print(applyGenConfigSubstitutions(sampleConfig, sessionSecret, encryptionKey, hash))
			return nil
		},
	}
	genConfig.Flags().BoolP("password", "p", false, "prompt for the root password and embed its bcrypt hash")

	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version and exit",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(version)
		},
	}

	root.AddCommand(serve, generate, hashPw, genConfig, versionCmd)
	return root
}

const sampleConfig = `# ntx.toml — ntx server configuration
# Sites are created and managed in the browser: sign in as "root" on
# platform_hostname. Run 'ntx gen-config -p' to set the root password.

bind_addr          = ""            # empty = localhost only; "0.0.0.0" listens on all interfaces
port               = 8080
db_dsn             = "sqlite://./ntx.db"
dev                = false

# Requests from these addresses may set X-Forwarded-For / X-Forwarded-Proto.
# Keep this to your reverse proxy; the default suits a proxy on this machine.
trusted_proxies    = ["127.0.0.1/32", "::1/128"]

session_secret     = ""            # required: random, min 32 chars (generated)
encryption_key     = ""            # required: 32 random bytes, base64 (generated)

themes_path        = "./themes"    # one folder per theme: <name>/templates, <name>/static
platform_hostname  = "localhost"   # where root signs in before any site exists
root_password_hash = ""            # required: bcrypt hash (ntx hash-password)
`

// app is the wired-up application shared by serve and generate.
type app struct {
	cfg      *config.Config
	registry *sitereg.Registry
	auth     *auth.Manager
	handler  *handlers.Handler
	close    func()
}

func openApp(cfgFile string) (*app, error) {
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return nil, err
	}
	database, err := db.Open(cfg.DBDSN)
	if err != nil {
		return nil, err
	}
	sqlDB, err := database.DB()
	if err != nil {
		return nil, err
	}
	closeDB := func() { sqlDB.Close() }

	users := models.NewUserStore(database)
	if _, err := users.EnsureRoot(); err != nil {
		closeDB()
		return nil, fmt.Errorf("ensuring root user: %w", err)
	}
	sites := models.NewSiteStore(database)
	reg, err := sitereg.New(sites)
	if err != nil {
		closeDB()
		return nil, fmt.Errorf("loading sites: %w", err)
	}
	mems := models.NewMembershipStore(database)
	ids := models.NewIdentityStore(database)
	invites := models.NewInviteStore(database)
	sessions := models.NewSessionStore(database)
	am := auth.NewManager(sessions, users, mems, cfg.RootPasswordHash, !cfg.Dev)
	h := handlers.New(handlers.Deps{
		Sites:            sites,
		Posts:            models.NewPostStore(database),
		Users:            users,
		Memberships:      mems,
		Identities:       ids,
		Invites:          invites,
		Sessions:         sessions,
		People:           people.New(users, ids, mems, invites),
		SSO:              sso.NewClient(&http.Client{Timeout: 10 * time.Second}),
		SessionSecret:    []byte(cfg.SessionSecret),
		EncryptionKey:    cfg.EncryptionKey,
		Registry:         reg,
		Auth:             am,
		Limiter:          auth.NewLimiter(),
		ThemesPath:       cfg.ThemesPath,
		PlatformHostname: cfg.PlatformHostname,
		RootPasswordHash: cfg.RootPasswordHash,
		Dev:              cfg.Dev,
	})
	return &app{cfg: cfg, registry: reg, auth: am, handler: h, close: closeDB}, nil
}

// hashRootPassword bcrypts a password typed for root_password_hash,
// refusing an empty one: root's password guards every site.
func hashRootPassword(pw string) (string, error) {
	if pw == "" {
		return "", fmt.Errorf("the password must not be empty")
	}
	return auth.HashPassword(pw)
}

// readPasswordLine reads one newline-terminated password from r for
// the non-terminal branch of hash-password (scripted/CI use, e.g.
// `echo "$PW" | ntx hash-password`). It rejects an empty result: a
// closed or empty stdin yields io.EOF with no bytes read, which would
// otherwise silently hash the empty string and print a well-formed
// bcrypt hash that authenticates an empty password -- with no warning
// that nothing was actually read. See task-10 addendum, fix round 1.
func readPasswordLine(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	pw := strings.TrimRight(line, "\r\n")
	if pw == "" {
		return "", fmt.Errorf("no password read from stdin")
	}
	return pw, nil
}

// applyGenConfigSubstitutions fills the generated secrets and, if one was
// computed, the root password hash into the sample config. Each anchor is
// the full `key = ""` text, which occurs exactly once in the sample.
func applyGenConfigSubstitutions(sample, sessionSecret, encryptionKey, rootHash string) string {
	out := strings.Replace(sample, `session_secret     = ""`, fmt.Sprintf(`session_secret     = "%s"`, sessionSecret), 1)
	out = strings.Replace(out, `encryption_key     = ""`, fmt.Sprintf(`encryption_key     = "%s"`, encryptionKey), 1)
	if rootHash != "" {
		out = strings.Replace(out, `root_password_hash = ""`, fmt.Sprintf(`root_password_hash = "%s"`, rootHash), 1)
	}
	return out
}

func randomSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}
