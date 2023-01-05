package dialogue

import (
	"context"
	"io"
)

type outKey struct{}

func OutFromContext(ctx context.Context) (io.Writer, bool) {
	v, ok := ctx.Value(outKey{}).(io.Writer)
	return v, ok
}
