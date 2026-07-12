package commands

// This file belongs to the update feature. Deletion recipes:
//   - Removing ALL update code (CHECK + REMOTE + SHARED): delete this file.
//   - Removing only REMOTE UPDATE: delete the fenced block below; the default
//     action should instead print manual update instructions, e.g.:
//       fmt.Println("Re-run the install script to update.")
// See internal/app/update_check.go and update_remote.go for full recipes.

import (
	"context"
	"errors"
	"fmt"
	"servo/internal/app"
	"servo/internal/platform/database/config"
	"servo/internal/types"

	"github.com/urfave/cli/v3"
)

var Update = register(func(a *app.App) *cli.Command {
	return &cli.Command{
		Name:  "update",
		Usage: "update the app",
		Flags: []cli.Flag{
			// --- BEGIN UPDATE CHECK ---
			&cli.BoolFlag{
				Name:  "notify",
				Usage: "toggle update notification",
			},
			&cli.BoolFlag{
				Name:  "check",
				Usage: "just check for updates",
			},
			// --- END UPDATE CHECK ---
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			// --- BEGIN UPDATE CHECK ---
			notify := cmd.Bool("notify")
			if notify {
				var updateNotifications bool
				if _, err := config.Update(a.DB, func(cfg *types.Configuration) error {
					cfg.UpdateNotifications = !cfg.UpdateNotifications
					updateNotifications = cfg.UpdateNotifications
					return nil
				}); err != nil {
					return fmt.Errorf("failed to update notification setting in config: %w", err)
				}
				// print status
				if updateNotifications {
					fmt.Println("Update notifications are now enabled.")
				} else {
					fmt.Println("Update notifications are now disabled.")
				}
				return nil
			}

			check := cmd.Bool("check")
			if check {
				if updateAvailable, err := a.CheckForUpdate(); err != nil {
					if errors.Is(err, app.ErrUpdatesDisabled) {
						fmt.Println("This install has updates disabled (no release-url file; mirror install?). Re-run the install script to update.")
						return nil
					}
					return fmt.Errorf("failed to check for updates: %w", err)
				} else if updateAvailable {
					fmt.Printf("Update available! Run '%s update' to update to the latest version.\n", a.BuildInfo().Name)
				} else {
					fmt.Println("No updates available.")
				}
				return nil
			}
			// --- END UPDATE CHECK ---

			return defaultUpdateAction(a)
		},
	}
})

// --- BEGIN REMOTE UPDATE ---
// defaultUpdateAction triggers the self-update on exit. When removing the
// REMOTE UPDATE block, delete this function and uncomment the manual variant
// below it.
func defaultUpdateAction(a *app.App) error {
	if err := a.DeferUpdate(); err != nil {
		if errors.Is(err, app.ErrUpdatesDisabled) {
			fmt.Println("This install has updates disabled (no release-url file; mirror install?). Re-run the install script to update.")
			return nil
		}
		return err
	}
	return nil
}

// --- END REMOTE UPDATE ---

// Manual variant — uncomment when the REMOTE UPDATE block is removed:
//
// func defaultUpdateAction(a *app.App) error {
// 	fmt.Println("Re-run the install script to update.")
// 	return nil
// }
