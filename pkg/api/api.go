package api

import (
	"net/http"
	"path"

	"github.com/Patagonicus/usdx-queue/pkg/auth"
	"github.com/Patagonicus/usdx-queue/pkg/backend"
	"github.com/Patagonicus/usdx-queue/pkg/log"
	"github.com/Patagonicus/usdx-reader/pkg/storage"
	"github.com/gorilla/mux"
)

func New(l log.Logger, authenticator auth.Authenticator, back *backend.Backend, songs storage.Backend, cl CoverLoader) (http.Handler, error) {
	r := mux.NewRouter()
	NewClients(l, authenticator, prefixRouter{r, "/clients"})
	NewTickets(l, authenticator, back, prefixRouter{r, "/tickets"})
	NewQueue(l, authenticator, back, prefixRouter{r, "/queue"})
	NewState(l, authenticator, back, prefixRouter{r, "/state"})
	err := NewSongs(l, authenticator, songs, cl, prefixRouter{r, "/songs"})
	if err != nil {
		return nil, err
	}
	return r, nil
}

func withPrefix(r *mux.Router, prefix string, handler http.Handler) {
	r.PathPrefix(prefix + "/").Handler(http.StripPrefix(prefix, handler))
}

type router interface {
	Handle(path string, h http.Handler) *mux.Route
}

type prefixRouter struct {
	r      *mux.Router
	prefix string
}

func (p prefixRouter) Handle(pt string, h http.Handler) *mux.Route {
	return p.r.Handle(path.Join(p.prefix, pt), h)
}
