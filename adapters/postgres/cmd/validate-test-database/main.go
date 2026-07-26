package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/JakeFAU/stacks/adapters/postgres/postgrestest"
)

const validationTimeout = 10 * time.Second

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), validationTimeout)
	defer cancel()
	if err := postgrestest.ValidateEnvironment(ctx); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "PostgreSQL integration preflight failed")
		os.Exit(1)
	}
}
