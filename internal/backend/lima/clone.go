package lima

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"

	"github.com/roshbhatia/ere/internal/backend"
	"github.com/roshbhatia/ere/internal/sandbox"
)

func (b *Backend) Clone(ctx context.Context, req sandbox.CloneRequest) (sandbox.Status, error) {
	if !b.Managed {
		return sandbox.Status{}, fmt.Errorf("cloning requires the managed lima provider")
	}
	if _, err := b.Validate(ctx, req.Spec); err != nil {
		return sandbox.Status{}, err
	}
	source, err := b.Status(ctx, sandbox.Ref{Name: req.Source})
	if err != nil {
		return source, err
	}
	if source.State != sandbox.StateStopped {
		return source, fmt.Errorf("template must be stopped")
	}
	original, err := b.loadSpec(req.Source)
	if err != nil {
		return source, err
	}
	if original.Workload != nil || original.Labels["ere.template"] != "prepared" {
		return source, fmt.Errorf("source must be a prepared Ere template without credentials")
	}
	if original.CPUs != req.Spec.CPUs || original.MemoryMB != req.Spec.MemoryMB || original.DiskGB != req.Spec.DiskGB || original.Architecture != req.Spec.Architecture {
		return source, fmt.Errorf("template and target resources must match")
	}
	if original.Image != req.Spec.Image {
		return source, fmt.Errorf("template and target image must match")
	}
	target, err := b.Status(ctx, sandbox.Ref{Name: req.Spec.Name})
	if err != nil {
		return target, err
	}
	if target.State != sandbox.StateAbsent {
		return target, fmt.Errorf("clone target already exists")
	}
	args := []string{"clone", instance(req.Source), instance(req.Spec.Name), "--tty=false", "--mount-none"}
	if _, err = backend.Check(ctx, b.Binary, args...); err != nil {
		return target, err
	}
	records, err := b.instances(ctx)
	if err != nil {
		return target, err
	}
	id, err := backend.LoadIdentity("lima|"+b.VMType, req.Spec.Name)
	if err != nil {
		return target, err
	}
	for _, record := range records {
		if record.Name == instance(req.Spec.Name) {
			// Rewrite mounts before first boot so clones cannot modify the source workspace.
			template, err := cloneMounts(filepath.Join(record.Dir, "lima.yaml"), b.template(req.Spec))
			if err != nil {
				return target, err
			}
			if err = os.WriteFile(filepath.Join(record.Dir, "lima.yaml"), template, 0o600); err != nil {
				return target, err
			}
			if err = os.WriteFile(filepath.Join(record.Dir, "ere-owner"), []byte(id.Owner), 0o600); err != nil {
				return target, err
			}
			if err = b.saveSpec(req.Spec); err != nil {
				return target, err
			}
			return b.Status(ctx, sandbox.Ref{Name: req.Spec.Name})
		}
	}
	return target, fmt.Errorf("cloned instance is absent")
}

func (b *Backend) SealTemplate(ctx context.Context, ref sandbox.Ref) (sandbox.Status, error) {
	status, err := b.Status(ctx, ref)
	if err != nil {
		return status, err
	}
	if status.State != sandbox.StateStopped {
		return status, fmt.Errorf("template must be stopped")
	}
	spec, err := b.loadSpec(ref.Name)
	if err != nil {
		return status, err
	}
	if spec.Workload != nil || spec.Labels["ere.template"] != "building" {
		return status, fmt.Errorf("instance is not a template build")
	}
	spec.Labels["ere.template"] = "prepared"
	if err = b.saveSpec(spec); err != nil {
		return status, err
	}
	return b.Status(ctx, ref)
}

func cloneMounts(path, target string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var original, desired yaml.Node
	if err = yaml.Unmarshal(data, &original); err != nil {
		return nil, err
	}
	if err = yaml.Unmarshal([]byte(target), &desired); err != nil {
		return nil, err
	}
	if len(original.Content) != 1 || original.Content[0].Kind != yaml.MappingNode || len(desired.Content) != 1 {
		return nil, fmt.Errorf("invalid clone configuration")
	}
	mapping := original.Content[0]
	for i := 0; i < len(desired.Content[0].Content); i += 2 {
		key, value := desired.Content[0].Content[i], desired.Content[0].Content[i+1]
		if key.Value != "mounts" && key.Value != "mountType" {
			continue
		}
		found := false
		for j := 0; j < len(mapping.Content); j += 2 {
			if mapping.Content[j].Value == key.Value {
				mapping.Content[j+1] = value
				found = true
				break
			}
		}
		if !found {
			mapping.Content = append(mapping.Content, key, value)
		}
	}
	return yaml.Marshal(&original)
}
