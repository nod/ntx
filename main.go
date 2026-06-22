package main

import (
	"fmt"
	"log"
	"os"

	"github.com/nerdtx/ntx/internal/auth"
	"github.com/nerdtx/ntx/internal/config"
	"github.com/nerdtx/ntx/internal/db"
	"github.com/nerdtx/ntx/internal/models"
	"github.com/nerdtx/ntx/internal/server"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	var cfgFile string

	root := &cobra.Command{
		Use:   "ntx",
		Short: "personal site server",
	}

	serve := &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP server",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return err
			}

			database, err := db.Open(cfg.DBPath)
			if err != nil {
				return err
			}
			defer database.Close()

			posts := models.NewPostStore(database)
			session := auth.NewSessionManager(cfg.SessionSecret)

			srv, err := server.New(cfg, posts, session)
			if err != nil {
				return err
			}

			log.Printf("listening on %s", srv.Addr)
			return srv.ListenAndServe()
		},
	}
	serve.Flags().StringVar(&cfgFile, "config", "", "config file (default: ntx.toml)")

	hashPw := &cobra.Command{
		Use:   "hash-password",
		Short: "Bcrypt a password for ntx.toml",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprint(os.Stderr, "Password: ")
			pw, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if err != nil {
				return err
			}
			hash, err := auth.HashPassword(string(pw))
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
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Print(sampleConfig)
		},
	}

	root.AddCommand(serve, hashPw, genConfig)
	return root
}

const sampleConfig = `# ntx.toml — ntx server configuration
# Run 'ntx hash-password' to generate admin_password_hash.
# Run 'openssl rand -base64 32' to generate session_secret.

site_name           = "mysite"
site_url            = "mysite.example.com"
site_description    = ""

static_path         = "./static"
db_path             = "./ntx.db"
port                = 8080
template_path       = "./templates"
about_slug          = "about"
admin_password_hash = ""   # required: bcrypt hash
session_secret      = ""   # required: random string, min 32 chars
dev                 = false
`
