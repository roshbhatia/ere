package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const ContractVersion = "sandbox/v2"

type Storage struct {
	Kind   string `json:"kind,omitempty" yaml:"kind,omitempty"`
	Source string `json:"source,omitempty" yaml:"source,omitempty"`
	Class  string `json:"class,omitempty" yaml:"class,omitempty"`
	SizeGB int    `json:"sizeGB,omitempty" yaml:"sizeGB,omitempty"`
}

type Workload struct {
	Argv      []string          `json:"argv"`
	Env       map[string]string `json:"env,omitempty"`
	Workdir   string            `json:"workdir,omitempty"`
	Provision []string          `json:"provision,omitempty"`
}

type Plan struct {
	Name      string `json:"name"`
	Backend   string `json:"backend"`
	Action    string `json:"action"`
	Digest    string `json:"digest"`
	Retention string `json:"retention"`
}

func Digest(value interface{}) string {
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

var (
	safeName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,45}[a-z0-9])?$`)
	envName  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

func Validate(spec Spec) error {
	if !safeName.MatchString(spec.Name) {
		return fmt.Errorf("sandbox name must be a lowercase DNS label of at most 47 characters")
	}
	if spec.MountPath != "" && (!strings.HasPrefix(spec.MountPath, "/") || strings.ContainsAny(spec.MountPath, "\n\r")) {
		return fmt.Errorf("mountPath must be an absolute path")
	}
	if spec.CPUs < 0 || spec.MemoryMB < 0 || spec.DiskGB < 0 || spec.Storage.SizeGB < 0 {
		return fmt.Errorf("resource sizes cannot be negative")
	}
	if spec.Workload != nil {
		if len(spec.Workload.Argv) == 0 {
			return fmt.Errorf("workload needs argv")
		}
		for key := range spec.Workload.Env {
			if !envName.MatchString(key) {
				return fmt.Errorf("invalid environment name %q", key)
			}
		}
	}
	return nil
}
