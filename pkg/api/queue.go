package api

import (
	"net/http"

	"github.com/Patagonicus/usdx-queue/pkg/auth"
	"github.com/Patagonicus/usdx-queue/pkg/backend"
	"github.com/Patagonicus/usdx-queue/pkg/httperr"
	"github.com/Patagonicus/usdx-queue/pkg/log"
	httpauth "github.com/Patagonicus/usdx-queue/pkg/middleware/auth"
	"github.com/Patagonicus/usdx-queue/pkg/middleware/httpjson"
)

type queueAPI struct {
	back *backend.Backend
	l    log.Logger
}

func NewQueue(l log.Logger, a auth.Authenticator, back *backend.Backend, router router) {
	requireList := httpauth.Require(l, a, auth.PermListQueue)
	requireAdvance := httpauth.Require(l, a, auth.PermAdvanceQueue)
	requireGoBack := httpauth.Require(l, a, auth.PermGoBackQueue)
	requirePause := httpauth.Require(l, a, auth.PermPauseQueue)

	q := queueAPI{
		back: back,
		l:    l,
	}

	router.Handle("", requireList(httperr.HandlerFunc(q.List))).Methods("GET")
	router.Handle("actions/advance", requireAdvance(httperr.HandlerFunc(q.Advance))).Methods("POST")
	router.Handle("actions/goback", requireGoBack(httperr.HandlerFunc(q.GoBack))).Methods("POST")
	router.Handle("actions/pause", requirePause(httperr.HandlerFunc(q.Pause))).Methods("POST")
}

func (q queueAPI) List(w http.ResponseWriter, r *http.Request) error {
	jw, _ := httpjson.Wrap(w, r)

	queue, err := q.back.GetQueue()
	if err != nil {
		return err
	}

	jw.Encode(queue)
	return nil
}

func (q queueAPI) Advance(w http.ResponseWriter, r *http.Request) error {
	err := q.back.Advance()
	switch {
	case err == backend.ErrInvalidQueueMovement:
		return httperr.WithCode(err, http.StatusConflict)
	case err != nil:
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (q queueAPI) GoBack(w http.ResponseWriter, r *http.Request) error {
	err := q.back.GoBack()
	switch {
	case err == backend.ErrInvalidQueueMovement:
		return httperr.WithCode(err, http.StatusConflict)
	case err != nil:
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (q queueAPI) Pause(w http.ResponseWriter, r *http.Request) error {
	err := q.back.Pause()
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
