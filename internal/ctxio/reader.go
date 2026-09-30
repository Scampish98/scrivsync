package ctxio

import (
	"context"
	"io"
)

type Reader struct {
	Context context.Context
	Source  io.Reader
}

func (r Reader) Read(b []byte) (int, error) {
	if err := r.Context.Err(); err != nil {
		return 0, err
	}

	return r.Source.Read(b)
}
