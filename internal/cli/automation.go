package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/roshbhatia/ere/internal/runner"
	"github.com/spf13/cobra"
)

type automationInput struct {
	Runner   string `json:"runner"`
	ID       string `json:"id,omitempty"`
	ThreadID string `json:"threadId,omitempty"`
	State    string `json:"state,omitempty"`
	Lines    int    `json:"lines,omitempty"`
}

func automate(ctx context.Context, e *runner.Engine, op string, in automationInput) (interface{}, error) {
	names := []string(nil)
	if in.Runner != "" {
		names = []string{in.Runner}
	}
	if op != "runner_profiles" && in.Runner == "" {
		return nil, fmt.Errorf("runner is required")
	}
	switch op {
	case "runner_profiles":
		type profile struct {
			Name     string `json:"name"`
			RunnerID string `json:"runnerId"`
			Provider string `json:"provider"`
		}
		profiles := []profile{}
		for _, r := range e.Config.Runners {
			profiles = append(profiles, profile{r.Name, r.RunnerID, r.Backend})
		}
		return profiles, nil
	case "runner_plan":
		return e.Plan(ctx, names)
	case "runner_ensure":
		if err := e.Up(ctx, names, false); err != nil {
			return nil, err
		}
		return e.Status(ctx, names)
	case "runner_status":
		return e.Status(ctx, names)
	case "runner_logs":
		return e.Logs(ctx, in.Runner, in.Lines)
	case "runner_acquire":
		return e.Allocation(ctx, in.Runner, "acquire", in.ID, "", "")
	case "runner_activity":
		return e.Allocation(ctx, in.Runner, "update", in.ID, in.ThreadID, in.State)
	case "runner_allocations":
		return e.Allocation(ctx, in.Runner, "list", "", "", "")
	case "runner_drain":
		return e.Allocation(ctx, in.Runner, "drain", "", "", "")
	case "runner_resume":
		return e.Allocation(ctx, in.Runner, "resume", "", "", "")
	case "runner_release":
		return e.Allocation(ctx, in.Runner, "update", in.ID, "", "released")
	default:
		return nil, fmt.Errorf("unknown operation %q", op)
	}
}

func newAPICmd(opts *options) *cobra.Command {
	var input string
	cmd := &cobra.Command{Use: "api <operation>", Short: "Call the automation engine with JSON input and output", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		var in automationInput
		if err := json.Unmarshal([]byte(input), &in); err != nil {
			return err
		}
		e, err := opts.engine(true)
		if err != nil {
			return err
		}
		out, err := automate(cmd.Context(), e, args[0], in)
		if err != nil {
			return err
		}
		return writeJSON(cmd.OutOrStdout(), out)
	}}
	cmd.Flags().StringVar(&input, "input", "{}", "JSON operation input (never include credentials)")
	return cmd
}

func newPlanCmd(opts *options) *cobra.Command {
	return &cobra.Command{Use: "plan [runner...]", Short: "Report required runner changes without creating compute", RunE: func(cmd *cobra.Command, args []string) error {
		e, err := opts.engine(false)
		if err != nil {
			return err
		}
		plans, err := e.Plan(cmd.Context(), args)
		if err != nil {
			return err
		}
		return writeJSON(cmd.OutOrStdout(), plans)
	}}
}

func newReconcileCmd(opts *options) *cobra.Command {
	var interval time.Duration
	cmd := &cobra.Command{Use: "reconcile [runner...]", Short: "Keep declared runners up until interrupted; never expire workspaces", RunE: func(cmd *cobra.Command, args []string) error {
		if interval < time.Second {
			return fmt.Errorf("interval must be at least one second")
		}
		for {
			e, err := opts.engine(true)
			if err != nil {
				return err
			}
			if err := e.Up(cmd.Context(), args, false); err != nil {
				return err
			}
			select {
			case <-cmd.Context().Done():
				return cmd.Context().Err()
			case <-time.After(interval):
			}
		}
	}}
	cmd.Flags().DurationVar(&interval, "interval", 30*time.Second, "reconciliation interval")
	return cmd
}

type (
	rpcRequest struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	rpcTool struct {
		Name        string                 `json:"name"`
		Description string                 `json:"description"`
		InputSchema map[string]interface{} `json:"inputSchema"`
		Annotations map[string]bool        `json:"annotations"`
	}
)

func automationTools() []rpcTool {
	tools := []rpcTool{}
	for _, op := range []string{"runner_profiles", "runner_plan", "runner_ensure", "runner_status", "runner_logs", "runner_acquire", "runner_activity", "runner_allocations", "runner_drain", "runner_resume", "runner_release"} {
		readOnly := op == "runner_profiles" || op == "runner_plan" || op == "runner_status" || op == "runner_logs" || op == "runner_allocations"
		required := []string{"runner"}
		if op == "runner_profiles" {
			required = []string{}
		}
		if op == "runner_activity" || op == "runner_release" {
			required = append(required, "id")
		}
		if op == "runner_activity" {
			required = append(required, "state")
		}
		tools = append(tools, rpcTool{Name: op, Description: "Ere " + op + " for configured runner profiles. Release retains compute and storage.", InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"runner": map[string]string{"type": "string"}, "id": map[string]string{"type": "string"}, "threadId": map[string]string{"type": "string"}, "state": map[string]string{"type": "string"}, "lines": map[string]string{"type": "integer"}}, "required": required, "additionalProperties": false}, Annotations: map[string]bool{"readOnlyHint": readOnly, "destructiveHint": false, "openWorldHint": true}})
	}
	return tools
}

func newMCPCmd(opts *options) *cobra.Command {
	return &cobra.Command{Use: "mcp", Short: "Serve the runner automation API over MCP stdio", RunE: func(cmd *cobra.Command, args []string) error {
		scanner := bufio.NewScanner(cmd.InOrStdin())
		scanner.Buffer(make([]byte, 65536), 4*1024*1024)
		encoder := json.NewEncoder(cmd.OutOrStdout())
		for scanner.Scan() {
			var req rpcRequest
			if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
				if err := encoder.Encode(map[string]interface{}{"jsonrpc": "2.0", "id": nil, "error": map[string]interface{}{"code": -32700, "message": "parse error"}}); err != nil {
					return err
				}
				continue
			}
			if req.JSONRPC != "2.0" || req.Method == "" {
				if err := encoder.Encode(map[string]interface{}{"jsonrpc": "2.0", "id": nil, "error": map[string]interface{}{"code": -32600, "message": "invalid request"}}); err != nil {
					return err
				}
				continue
			}
			if len(req.ID) == 0 {
				continue
			}
			result, err := rpcCall(cmd.Context(), opts, req)
			response := map[string]interface{}{"jsonrpc": "2.0", "id": req.ID}
			if err != nil {
				code := -32602
				if errors.Is(err, errMethodNotFound) {
					code = -32601
				}
				response["error"] = map[string]interface{}{"code": code, "message": err.Error()}
			} else {
				response["result"] = result
			}
			if err := encoder.Encode(response); err != nil {
				return err
			}
		}
		return scanner.Err()
	}}
}

var errMethodNotFound = errors.New("method not found")

func rpcCall(ctx context.Context, opts *options, req rpcRequest) (interface{}, error) {
	switch req.Method {
	case "initialize":
		return map[string]interface{}{"protocolVersion": "2025-06-18", "capabilities": map[string]interface{}{"tools": map[string]interface{}{}}, "serverInfo": map[string]string{"name": "ere", "version": "0.1.0"}}, nil
	case "ping":
		return map[string]interface{}{}, nil
	case "tools/list":
		return map[string]interface{}{"tools": automationTools()}, nil
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments automationInput `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, err
		}
		e, err := opts.engine(false)
		if err != nil {
			return nil, err
		}
		out, callErr := automate(ctx, e, params.Name, params.Arguments)
		text := ""
		if callErr != nil {
			text = callErr.Error()
		} else {
			data, err := json.Marshal(out)
			if err != nil {
				return nil, err
			}
			text = string(data)
		}
		return map[string]interface{}{"content": []map[string]string{{"type": "text", "text": text}}, "isError": callErr != nil}, nil
	default:
		return nil, fmt.Errorf("%w: %q", errMethodNotFound, req.Method)
	}
}
