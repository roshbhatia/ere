package sandbox

import "context"

const OpConnect = "connect"

type (
	ConnectionRequest struct {
		Name    string   `json:"name"`
		Workdir string   `json:"workdir"`
		Argv    []string `json:"argv,omitempty"`
	}
	Connection struct {
		Argv []string `json:"argv"`
	}
	Connector interface {
		Connect(context.Context, ConnectionRequest) (Connection, error)
	}
)

func (c *Client) Connect(ctx context.Context, req ConnectionRequest) (Connection, error) {
	var out Connection
	err := c.call(ctx, OpConnect, req, &out)
	return out, err
}
