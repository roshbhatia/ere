package backend

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/roshbhatia/ere/internal/sandbox"
)

type Identity struct {
	Owner  string `json:"owner"`
	UID    string `json:"uid,omitempty"`
	Digest string `json:"digest,omitempty"`
	Path   string `json:"-"`
}

func LoadIdentity(scope, name string) (Identity, error) {
	root := os.Getenv("XDG_STATE_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Identity{}, err
		}
		root = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(root, "ere", "resources", sandbox.Digest(scope))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Identity{}, err
	}
	path := filepath.Join(dir, sandbox.Digest(name)+".json")
	data, err := os.ReadFile(path)
	if err == nil {
		var id Identity
		if err := json.Unmarshal(data, &id); err != nil {
			return id, err
		}
		id.Path = path
		return id, nil
	}
	if !os.IsNotExist(err) {
		return Identity{}, err
	}
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return Identity{}, err
	}
	id := Identity{Owner: hex.EncodeToString(bytes), Path: path}
	encoded, err := json.Marshal(id)
	if err != nil {
		return id, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		return LoadIdentity(scope, name)
	}
	if err != nil {
		return id, err
	}
	_, err = file.Write(encoded)
	closeErr := file.Close()
	if err != nil {
		return id, err
	}
	return id, closeErr
}

func (id Identity) Save() error {
	if id.Path == "" {
		return fmt.Errorf("identity has no storage path")
	}
	data, err := json.Marshal(id)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(id.Path), ".identity-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), id.Path)
}
