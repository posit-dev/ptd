package containers

import (
	"context"
	"io"
	"os"

	"github.com/containers/image/v5/copy"
	"github.com/containers/image/v5/signature"
	"github.com/containers/image/v5/types"
)

type Image struct {
	SystemContext types.SystemContext
	Ref           types.ImageReference
}

func NewPolicyContext() (*signature.PolicyContext, error) {
	policy := &signature.Policy{Default: []signature.PolicyRequirement{signature.NewPRInsecureAcceptAnything()}}
	return signature.NewPolicyContext(policy)
}

func NewDockerAuthSystemContext(username string, password string) (sc types.SystemContext) {
	sc.DockerAuthConfig = &types.DockerAuthConfig{
		Username: username,
		Password: password,
	}
	return
}

func CopyImage(ctx context.Context, src Image, dst Image, writer io.Writer) error {
	policyContext, err := NewPolicyContext()
	if err != nil {
		return err
	}

	if writer == nil {
		writer = os.Stdout
	}

	_, err = copy.Image(ctx, policyContext, dst.Ref, src.Ref, &copy.Options{
		SourceCtx:      &src.SystemContext,
		DestinationCtx: &dst.SystemContext,
		ReportWriter:   writer,
	})
	return err
}
