package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/roshbhatia/ere/internal/config"
	"github.com/roshbhatia/ere/internal/sandbox"
	"github.com/roshbhatia/go-utils/paths"

	"github.com/roshbhatia/ere/internal/events"
	"github.com/roshbhatia/ere/internal/runner"
	"github.com/roshbhatia/ere/internal/secret"
	"github.com/spf13/cobra"
)

func newEventsCmd(opts *options) *cobra.Command {
	var path string
	root := &cobra.Command{Use: "events", Short: "Queue and process durable private-runner events"}
	root.PersistentFlags().StringVar(&path, "queue", "", "queue file (default: XDG state)")
	var task runner.ThreadOptions
	var id string
	var after, every time.Duration
	enqueue := &cobra.Command{Use: "enqueue <runner> <prompt>", Short: "Queue an idempotent task, optionally on a timer", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		q, err := eventQueue(opts, path)
		if err != nil {
			return err
		}
		e, err := opts.engine(false)
		if err != nil {
			return err
		}
		r, ok := e.Config.Runner(args[0])
		if !ok {
			return fmt.Errorf("unknown runner %q", args[0])
		}
		task.Runner = args[0]
		task.Prompt = args[1]
		j, err := q.Enqueue(events.Job{ID: id, Route: events.Route{RunnerID: r.RunnerID, Provider: r.Backend}, Task: task, Due: time.Now().Add(after), Every: every})
		if err != nil {
			return err
		}
		return writeJSON(cmd.OutOrStdout(), j)
	}}
	enqueue.Flags().StringVar(&id, "id", "", "required stable event ID")
	enqueue.Flags().StringVar(&task.Mode, "mode", "", "Amp mode")
	enqueue.Flags().DurationVar(&after, "after", 0, "delay before first execution")
	enqueue.Flags().DurationVar(&every, "every", 0, "repeat interval after completion")
	root.AddCommand(enqueue)
	root.AddCommand(&cobra.Command{Use: "list", Short: "Show queued events and recovery states", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		q, err := eventQueue(opts, path)
		if err != nil {
			return err
		}
		jobs, err := q.List()
		if err != nil {
			return err
		}
		return writeJSON(cmd.OutOrStdout(), jobs)
	}})
	root.AddCommand(&cobra.Command{Use: "cancel <id>", Short: "Cancel an event that has not been submitted", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		q, err := eventQueue(opts, path)
		if err != nil {
			return err
		}
		return q.Cancel(args[0])
	}})
	root.AddCommand(&cobra.Command{Use: "recover <event-id> <thread-id> <verified-allocation-id>", Short: "Bind an interrupted event after checking its allocation label in Amp", Args: cobra.ExactArgs(3), RunE: func(cmd *cobra.Command, args []string) error {
		q, err := eventQueue(opts, path)
		if err != nil {
			return err
		}
		e, err := opts.engine(false)
		if err != nil {
			return err
		}
		return q.Recover(cmd.Context(), e, args[0], args[1], args[2])
	}})
	var once bool
	worker := &cobra.Command{Use: "work", Short: "Process queued events and observe their threads", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		q, err := eventQueue(opts, path)
		if err != nil {
			return err
		}
		e, err := opts.engine(true)
		if err != nil {
			return err
		}
		for {
			if err = q.Work(cmd.Context(), e); err != nil {
				return err
			}
			if once {
				return nil
			}
			select {
			case <-cmd.Context().Done():
				return nil
			case <-time.After(5 * time.Second):
			}
		}
	}}
	worker.Flags().BoolVar(&once, "once", false, "process one queue iteration")
	root.AddCommand(worker)
	var listen, reference, profile, promptFile string
	serve := &cobra.Command{Use: "serve", Short: "Accept signed JSON webhooks into the durable queue", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if reference == "" || profile == "" || promptFile == "" {
			return fmt.Errorf("--secret, --runner, and --prompt-file are required")
		}
		e, err := opts.engine(false)
		if err != nil {
			return err
		}
		r, ok := e.Config.Runner(profile)
		if !ok {
			return fmt.Errorf("unknown runner %q", profile)
		}
		key, err := secret.New(opts.opBinary).Resolve(cmd.Context(), reference)
		if err != nil {
			return err
		}
		if key == "" {
			return fmt.Errorf("webhook secret is empty")
		}
		prompt, err := os.ReadFile(promptFile)
		if err != nil {
			return err
		}
		q, err := eventQueue(opts, path)
		if err != nil {
			return err
		}
		server := &http.Server{Addr: listen, Handler: events.Handler(q, []byte(key), profile, string(prompt), events.Route{RunnerID: r.RunnerID, Provider: r.Backend}), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
		go func() {
			<-cmd.Context().Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(ctx)
		}()
		fmt.Fprintf(cmd.ErrOrStderr(), "Webhook receiver listening on %s; run ere events work to process events\n", listen)
		err = server.ListenAndServe()
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}}
	serve.Flags().StringVar(&listen, "listen", "127.0.0.1:8787", "listen address; use an authenticated TLS proxy for remote access")
	serve.Flags().StringVar(&reference, "secret", "", "HMAC secret reference")
	serve.Flags().StringVar(&profile, "runner", "", "fixed target profile")
	serve.Flags().StringVar(&promptFile, "prompt-file", "", "trusted instructions; request body is appended as data")
	root.AddCommand(serve)
	return root
}

func eventQueue(opts *options, path string) (*events.Queue, error) {
	if path == "" {
		cfg, err := config.Path(opts.configPath)
		if err != nil {
			return nil, err
		}
		cfg, err = filepath.Abs(cfg)
		if err != nil {
			return nil, err
		}
		path = filepath.Join(paths.StateHome(), "ere", "events", sandbox.Digest(cfg)+".json")
	}
	return events.Open(path)
}
