package registryserver

import (
	"context"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tsnet"

	"github.com/scotmcc/cairo2/internal/authn"
)

type tsnetAdapter struct {
	srv *tsnet.Server
}

func (a *tsnetAdapter) WhoIs(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
	lc, err := a.srv.LocalClient()
	if err != nil {
		return nil, err
	}
	return lc.WhoIs(ctx, remoteAddr)
}

func (s *Server) resolver() authn.Resolver {
	if s.tsnetSrv == nil {
		return nil
	}
	return &tsnetAdapter{srv: s.tsnetSrv}
}
