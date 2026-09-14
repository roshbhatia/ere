package sandbox

import "context"

const OpClone = "clone"

type (
	CloneRequest struct {
		Source string `json:"source"`
		Spec   Spec   `json:"spec"`
	}
	Cloner interface {
		Clone(context.Context, CloneRequest) (Status, error)
	}
)

func (c *Client) Clone(ctx context.Context, req CloneRequest) (Status, error) {
	var out Status
	err := c.call(ctx, OpClone, req, &out)
	return out, err
}

const OpSealTemplate = "seal-template"

type TemplateSealer interface {
	SealTemplate(context.Context, Ref) (Status, error)
}

func (c *Client) SealTemplate(ctx context.Context, ref Ref) (Status, error) {
	var out Status
	err := c.call(ctx, OpSealTemplate, ref, &out)
	return out, err
}
