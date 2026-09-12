// Package sandbox defines the wire contract between lifier and a sandbox
// backend. A backend is an external executable that speaks provider/v1 frames,
// so the types here are the payloads, not an in-process interface boundary.
package sandbox

import "context"

const (
	// Kind narrows provider/v1's opaque manifest kind to this contract.
	Kind = "sandbox"
	// Capability is the manifest action name every backend must declare.
	Capability = "sandbox"
)

// Operations a backend may implement. Probe reports which ones it serves.
const (
	OpProbe   = "probe"
	OpCreate  = "create"
	OpStart   = "start"
	OpExec    = "exec"
	OpStatus  = "status"
	OpList    = "list"
	OpLogs    = "logs"
	OpStop    = "stop"
	OpDestroy = "destroy"
)

// State is the lifecycle position of one sandbox.
type State string

const (
	StateAbsent   State = "absent"
	StateStopped  State = "stopped"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateUnknown  State = "unknown"
)

// Spec is the sandbox a backend is asked to materialize. It says nothing about
// Amp: the agent reaches the sandbox through Exec like any other command.
type Spec struct {
	Name      string            `json:"name"`
	Image     string            `json:"image,omitempty"`
	Workspace string            `json:"workspace,omitempty"`
	MountPath string            `json:"mountPath,omitempty"`
	ReadOnly  bool              `json:"readOnly,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	CPUs      int               `json:"cpus,omitempty"`
	MemoryMB  int               `json:"memoryMB,omitempty"`
	DiskGB    int               `json:"diskGB,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// Ref names an existing sandbox.
type Ref struct {
	Name string `json:"name"`
}

// ExecRequest runs one command inside a running sandbox.
type ExecRequest struct {
	Name    string            `json:"name"`
	Argv    []string          `json:"argv"`
	Workdir string            `json:"workdir,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Detach  bool              `json:"detach,omitempty"`
	LogFile string            `json:"logFile,omitempty"`
}

// ExecResult is the outcome of a foreground Exec. A detached Exec returns
// ExitCode 0 once the process is launched.
type ExecResult struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
}

// LogRequest asks for a bounded tail. There is no follow mode: a provider/v1
// invocation is one request and one result, not a stream held open.
type LogRequest struct {
	Name  string `json:"name"`
	Lines int    `json:"lines,omitempty"`
}

// Logs is a bounded tail of sandbox output.
type Logs struct {
	Lines []string `json:"lines"`
}

// Status is what a backend knows about one sandbox.
type Status struct {
	Name    string `json:"name"`
	Backend string `json:"backend"`
	State   State  `json:"state"`
	Image   string `json:"image,omitempty"`
	Address string `json:"address,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// List is every sandbox a backend owns on this host.
type List struct {
	Sandboxes []Status `json:"sandboxes"`
}

// Probe reports whether the backend can run here and what it implements.
type Probe struct {
	Backend    string   `json:"backend"`
	Available  bool     `json:"available"`
	Operations []string `json:"operations"`
	Detail     string   `json:"detail,omitempty"`
}

// Backend is the in-process shape a shipped backend implements. The serve
// layer projects it onto the wire; an external backend implements the wire
// directly and never sees this type.
type Backend interface {
	Name() string
	Probe(ctx context.Context) (Probe, error)
	Create(ctx context.Context, spec Spec) (Status, error)
	Start(ctx context.Context, ref Ref) (Status, error)
	Exec(ctx context.Context, req ExecRequest) (ExecResult, error)
	Status(ctx context.Context, ref Ref) (Status, error)
	List(ctx context.Context) (List, error)
	Logs(ctx context.Context, req LogRequest) (Logs, error)
	Stop(ctx context.Context, ref Ref) (Status, error)
	Destroy(ctx context.Context, ref Ref) (Status, error)
}

// Operations is the full set, in lifecycle order.
func Operations() []string {
	return []string{OpProbe, OpCreate, OpStart, OpExec, OpStatus, OpList, OpLogs, OpStop, OpDestroy}
}
