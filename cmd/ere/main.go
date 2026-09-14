// Command ere runs Amp runners in sandboxes: containers, local virtual
// machines, or whatever a provider/v1 backend manifest points at.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/roshbhatia/ere/internal/cli"
)

// version is set at build time via -ldflags "-X main.version=<tag>".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cli.NewRootCmd(version).ExecuteContext(ctx); err != nil {
		// A command run inside a sandbox keeps its own exit code, so a caller can
		// branch on the sandboxed failure rather than on ere's.
		var exit *cli.ExitError
		if errors.As(err, &exit) {
			os.Exit(exit.Code)
		}
		fmt.Fprintln(os.Stderr, "ere: "+err.Error())
		os.Exit(1)
	}
}
