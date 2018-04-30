package auth

import (
	"context"
	"net/http"

	"github.com/Patagonicus/usdx-queue/pkg/auth"
	"github.com/Patagonicus/usdx-queue/pkg/log"
)

type key struct{}

var clientKey key

type Auth interface {
	Get(t auth.Token) (auth.Client, error)
}

func Require(l log.Logger, a Auth, p auth.Permission) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		return RequireOne(l, a, New(h, p))
	}
}

func RequireOne(l log.Logger, a Auth, hs ...PermissionHandler) http.Handler {
	l = l.With(log.Any("needed", hs))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, token, ok := r.BasicAuth()
		if !ok {
			l.Debug("got request without authentication")
			w.Header().Set("WWW-Authenticate", `Basic realm="usdx-queue"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		client, err := a.Get(auth.Token(token))
		if err != nil {
			if _, ok := err.(auth.ErrNotFound); ok {
				w.Header().Set("WWW-Authenticate", `Basic realm="usdx-queue"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			l.Debug("got invalid token",
				log.String("token", token),
			)
			w.Header().Set("WWW-Authenticate", `Basic realm="usdx-queue"`)
			http.Error(w, "Failed to lookup your token", http.StatusInternalServerError)
			return
		}
		l := l.With(log.Stringer("client", client))

		var handler http.Handler
		for _, h := range hs {
			if h.HasPermission(client) {
				handler = h
				break
			}
		}

		if handler == nil {
			l.Debug("client not authorized")
			w.Header().Set("WWW-Authenticate", `Basic realm="usdx-queue"`)
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		l.Debug("client is authorized",
			log.Any("handler", handler),
		)
		r = r.WithContext(SetClient(r.Context(), client))
		handler.ServeHTTP(w, r)
	})
}

type PermissionHandler interface {
	HasPermission(c auth.Client) bool
	ServeHTTP(w http.ResponseWriter, r *http.Request)
}

type handler struct {
	h http.Handler
	p auth.Permission
}

func (h handler) HasPermission(c auth.Client) bool {
	return h.p.HasPermission(c)
}

func (h handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.h.ServeHTTP(w, r)
}

func New(h http.Handler, p auth.Permission) PermissionHandler {
	return handler{h, p}
}

func SetClient(ctx context.Context, client auth.Client) context.Context {
	return context.WithValue(ctx, clientKey, client)
}

func GetClient(ctx context.Context) (auth.Client, bool) {
	v := ctx.Value(clientKey)
	switch vv := v.(type) {
	case auth.Client:
		return vv, true
	default:
		return auth.Client{}, false
	}
}
