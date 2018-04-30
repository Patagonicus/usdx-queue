package api

import (
	"net/http"

	"github.com/Patagonicus/usdx-queue/pkg/auth"
	"github.com/Patagonicus/usdx-queue/pkg/backend"
	"github.com/Patagonicus/usdx-queue/pkg/httperr"
	"github.com/Patagonicus/usdx-queue/pkg/log"
	httpauth "github.com/Patagonicus/usdx-queue/pkg/middleware/auth"
	"github.com/Patagonicus/usdx-queue/pkg/middleware/httpjson"
	"github.com/Patagonicus/usdx-queue/pkg/model"
)

type stateAPI struct {
	back *backend.Backend
	l    log.Logger
}

func NewState(l log.Logger, a auth.Authenticator, back *backend.Backend, router router) {
	requireRead := httpauth.Require(l, a, auth.PermListState)
	requireSet := httpauth.Require(l, a, auth.PermSetState)

	s := stateAPI{
		back: back,
		l:    l,
	}

	router.Handle("/", requireRead(httperr.HandlerFunc(s.List))).Methods("GET")
	router.Handle("/", requireSet(httperr.HandlerFunc(s.Set))).Methods("PUT", "POST")
}

func (s stateAPI) List(w http.ResponseWriter, r *http.Request) error {
	jw, _ := httpjson.Wrap(w, r)

	state, err := s.back.GetState()
	if err != nil {
		s.l.Warn("error getting state",
			log.Error(err),
		)
		return err
	}

	jw.Encode(state)
	return nil
}

func (s stateAPI) Set(w http.ResponseWriter, r *http.Request) error {
	jw, jr := httpjson.Wrap(w, r)

	var state model.State
	s.l.Debug("decoding state")
	err := jr.Decode(&state)
	if err != nil {
		s.l.Debug("failed")
		return httperr.WithCode(err, http.StatusBadRequest)
	}

	s.l.Debug("setting state")
	err = s.back.UpdateState(state)
	if err != nil {
		s.l.Debug("failed")
		return err
	}

	s.l.Debug("writing header")
	jw.WriteHeader(http.StatusNoContent)
	return nil
}
