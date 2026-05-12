package authn

import (
	"context"
	"net/http"

	"tailscale.com/client/tailscale/apitype"
)

type Identity struct {
	User    string
	Source  string
	NodeKey string
	Tags    []string
}

type Resolver interface {
	WhoIs(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error)
}

func VerifyWith(r *http.Request, resolver Resolver) (Identity, error) {
	if resolver != nil {
		resp, err := resolver.WhoIs(r.Context(), r.RemoteAddr)
		if err == nil && resp != nil {
			var loginName string
			if resp.UserProfile != nil {
				loginName = resp.UserProfile.LoginName
			}
			var nodeKey string
			var tags []string
			if resp.Node != nil {
				nodeKey = resp.Node.Key.String()
				tags = resp.Node.Tags
			}
			if loginName != "" {
				return Identity{User: loginName, Source: "tsnet", NodeKey: nodeKey, Tags: tags}, nil
			}
			if len(tags) > 0 {
				return Identity{User: tags[0], Source: "tsnet", NodeKey: nodeKey, Tags: tags}, nil
			}
		}
	}
	if v := r.Header.Get("X-Operator-Identity"); v != "" {
		return Identity{User: v, Source: "header"}, nil
	}
	return Identity{User: "local", Source: "local"}, nil
}

func Verify(r *http.Request) (Identity, error) {
	return VerifyWith(r, nil)
}
