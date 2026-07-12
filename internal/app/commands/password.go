package commands

import (
	"context"
	"fmt"
	"servo/internal/app"
	"servo/internal/platform/database/config"
	"servo/internal/types"
	"servo/pkg/crypto"
	"strings"

	"github.com/urfave/cli/v3"
)

var Password = register(func(a *app.App) *cli.Command {
	return &cli.Command{
		Name:  "password",
		Usage: "manage UI passwords",
		Commands: []*cli.Command{
			{
				Name:  "add",
				Usage: "add a new password credential",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "label",
						Usage:    "unique label for this credential",
						Required: true,
					},
					&cli.StringFlag{
						Name:  "password",
						Usage: "plaintext password (omit to be prompted without echo; the flag leaks into shell history and ps)",
					},
					&cli.StringFlag{
						Name:  "perms",
						Usage: `space-separated permissions (e.g. "admin", "admin !server.control", "settings")`,
						Value: "admin",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					label := strings.TrimSpace(cmd.String("label"))
					plain := cmd.String("password")
					permStr := cmd.String("perms")

					if label == "" {
						return fmt.Errorf("label cannot be empty")
					}
					if plain == "" {
						var err error
						if plain, err = readSecret("Password: "); err != nil {
							return err
						}
					}
					if plain == "" {
						return fmt.Errorf("password cannot be empty")
					}

					perms, err := types.ParsePerms(strings.Fields(permStr))
					if err != nil {
						return fmt.Errorf("invalid perms: %w", err)
					}

					passHash, passSalt, err := crypto.HashPassword(plain)
					if err != nil {
						return fmt.Errorf("failed to hash password: %w", err)
					}

					if _, err := config.Update(a.DB, func(cfg *types.Configuration) error {
						for _, c := range cfg.Credentials {
							if c.Label == label {
								return fmt.Errorf("credential with label %q already exists", label)
							}
						}
						cfg.Credentials = append(cfg.Credentials, types.Credential{
							Label:    label,
							PassHash: passHash,
							PassSalt: passSalt,
							Perms:    perms,
						})
						return nil
					}); err != nil {
						return err
					}

					fmt.Printf("Added credential %q with perms: %s\n", label, perms)
					return nil
				},
			},
			{
				Name:  "list",
				Usage: "list all password credentials",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, err := config.View(a.DB)
					if err != nil {
						return fmt.Errorf("failed to read config: %w", err)
					}
					if len(cfg.Credentials) == 0 {
						fmt.Println("No credentials configured.")
						return nil
					}
					for _, c := range cfg.Credentials {
						fmt.Printf("  %s  [%s]\n", c.Label, c.Perms)
					}
					return nil
				},
			},
			{
				Name:  "remove",
				Usage: "remove a password credential by label",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "label",
						Usage:    "label of the credential to remove",
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					label := strings.TrimSpace(cmd.String("label"))

					if _, err := config.Update(a.DB, func(cfg *types.Configuration) error {
						for i, c := range cfg.Credentials {
							if c.Label == label {
								cfg.Credentials = append(cfg.Credentials[:i], cfg.Credentials[i+1:]...)
								return nil
							}
						}
						return fmt.Errorf("credential %q not found", label)
					}); err != nil {
						return err
					}

					fmt.Printf("Removed credential %q\n", label)
					return nil
				},
			},
		},
	}
})
