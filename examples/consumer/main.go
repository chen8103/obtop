package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/chen8103/obtop"
)

// Example consumer module that imports the public SDK only.
// Build: go run . -h <host> -P <port> -u <user> -p <password>
func main() {
	host := envOr("OBTOP_HOST", "127.0.0.1")
	user := os.Getenv("OBTOP_USER")
	pass := os.Getenv("OBTOP_PASSWORD")
	if user == "" || pass == "" {
		fmt.Fprintln(os.Stderr, "set OBTOP_USER and OBTOP_PASSWORD")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := obtop.Open(ctx, obtop.Config{
		Host:     host,
		Port:     3306,
		User:     user,
		Password: pass,
		Database: "oceanbase",
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer client.Close()

	snap, err := client.Snapshot(ctx, obtop.SnapshotOptions{ActiveOnly: true})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("sampled_at=%s sessions=%d warming_up=%v stale=%v\n",
		snap.SampledAt.Format(time.RFC3339),
		len(snap.Filtered),
		snap.RatesWarmingUp,
		snap.RatesStale,
	)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
