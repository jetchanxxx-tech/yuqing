package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yuqing/platform/internal/config"
	"github.com/yuqing/platform/internal/pkg/storage"
	"github.com/yuqing/platform/internal/platform/accountclosure"
)

// Operator execution is a bounded step using the prebuilt artifact. It cannot
// skip the withdrawal deadline, fresh blockers, ownership or retention rules.
func handleAccountClosure(args []string) {
	if len(args) < 1 || (args[0] != "preview" && args[0] != "execute" && args[0] != "retry") {
		fmt.Fprintln(os.Stderr, "usage: yuqing-cli account-closure <preview|execute|retry> --user-id <immutable-id> [--limit 100]")
		os.Exit(1)
	}
	flags := flag.NewFlagSet("account-closure", flag.ExitOnError)
	uid := flags.String("user-id", "", "immutable user ID")
	limit := flags.Int("limit", 100, "maximum objects per cleanup step")
	_ = flags.Parse(args[1:])
	if *uid == "" || flags.NArg() != 0 || *limit < 1 || *limit > 1000 {
		fmt.Fprintln(os.Stderr, "account-closure: user ID and a limit from 1 to 1000 are required")
		os.Exit(1)
	}
	cfg, err := config.Load(configPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, "account-closure: configuration unavailable")
		os.Exit(1)
	}
	if cfg.Store.Driver != "postgres" {
		fmt.Fprintln(os.Stderr, "account-closure: persistent PostgreSQL storage required")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, cfg.DB.Primary)
	if err != nil {
		fmt.Fprintln(os.Stderr, "account-closure: storage unavailable")
		os.Exit(1)
	}
	defer pool.Close()
	store := accountclosure.NewPGStore(pool)
	root := cfg.Storage.AvatarRoot
	if root == "" {
		root = "data/avatars"
	}
	store.SetAvatarStorage(storage.NewLocalAvatar(root))
	var result any
	if args[0] == "preview" {
		var version int64
		err = pool.QueryRow(ctx, `SELECT token_version FROM users WHERE id=$1`, *uid).Scan(&version)
		if err == nil {
			result, err = store.Preview(ctx, *uid, version)
		}
	} else {
		result, err = store.Process(ctx, *uid, *limit)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "account-closure: operation did not finish; inspect stored progress before retry")
		os.Exit(1)
	}
	if err = json.NewEncoder(os.Stdout).Encode(result); err != nil {
		os.Exit(1)
	}
}
