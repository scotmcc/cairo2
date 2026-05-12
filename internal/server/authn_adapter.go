package server

import (
	"context"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tsnet"

	"github.com/scotmcc/cairo2/internal/authn"
)

type tsnetResolver struct{ srv *tsnet.Server }

func (t *tsnetResolver) WhoIs(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
	lc, err := t.srv.LocalClient()
	if err != nil {
		return nil, err
	}
	return lc.WhoIs(ctx, remoteAddr)
}

func NewResolver(srv *tsnet.Server) authn.Resolver {
	if srv == nil {
		return nil
	}
	return &tsnetResolver{srv: srv}
}
