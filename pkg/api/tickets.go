package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/Patagonicus/usdx-queue/pkg/auth"
	"github.com/Patagonicus/usdx-queue/pkg/backend"
	"github.com/Patagonicus/usdx-queue/pkg/httperr"
	"github.com/Patagonicus/usdx-queue/pkg/log"
	httpauth "github.com/Patagonicus/usdx-queue/pkg/middleware/auth"
	"github.com/Patagonicus/usdx-queue/pkg/middleware/httpjson"
	"github.com/Patagonicus/usdx-queue/pkg/model"
	"github.com/gorilla/mux"
)

type tickets struct {
	back *backend.Backend
	l    log.Logger
}

func NewTickets(l log.Logger, a auth.Authenticator, back *backend.Backend, router router) {
	requireList := httpauth.Require(l, a, auth.PermListTickets)
	requireCreate := httpauth.Require(l, a, auth.PermCreateTicket)

	t := tickets{
		back: back,
		l:    l,
	}

	router.Handle("/", requireList(httperr.HandlerFunc(t.List))).Methods("GET")
	router.Handle("/{id}", requireList(httperr.HandlerFunc(t.Get))).Methods("GET")
	router.Handle("/{id}", httpauth.RequireOne(l, a,
		httpauth.New(httperr.HandlerFunc(t.SetNames), auth.PermSetNames),
		httpauth.New(httperr.HandlerFunc(t.SetNamesWithPIN), auth.PermSetNamesWithPIN),
	)).Methods("PATCH")
	router.Handle("/", requireCreate(httperr.HandlerFunc(t.Create))).Methods("POST")
}

func (t tickets) List(w http.ResponseWriter, r *http.Request) error {
	jw, _ := httpjson.Wrap(w, r)

	ts, err := t.back.GetTickets()
	if err != nil {
		return err
	}

	result := make([]model.Ticket, 0, len(ts))
	for _, v := range ts {
		result = append(result, v)
	}

	return jw.Encode(result)
}

func (t tickets) Get(w http.ResponseWriter, r *http.Request) error {
	jw, _ := httpjson.Wrap(w, r)

	idS, ok := mux.Vars(r)["id"]
	if !ok {
		t.l.Debug("got request without ticket id")
		return httperr.WithCode(errors.New("ticket id missing"), http.StatusBadRequest)
	}

	ticket, err := t.back.GetTicket(model.ID(idS))
	switch err.(type) {
	case nil:
	case backend.ErrTicketDoesNotExist:
		return httperr.WithCode(err, http.StatusNotFound)
	default:
		return err
	}

	return jw.Encode(ticket)
}

func (t tickets) Create(w http.ResponseWriter, r *http.Request) error {
	jw, _ := httpjson.Wrap(w, r)

	ticket, pin, err := t.back.CreateTicket()
	if err != nil {
		return err
	}

	jw.Header().Set("Location", string(ticket.ID))
	jw.WriteHeader(http.StatusCreated)
	jw.Encode(struct {
		PIN model.PIN `json:"pin"`
	}{
		pin,
	})
	return nil
}

func (t tickets) SetNamesWithPIN(w http.ResponseWriter, r *http.Request) error {
	jw, jr := httpjson.Wrap(w, r)

	idS, ok := mux.Vars(r)["id"]
	if !ok {
		t.l.Debug("got request without ticket id")
		return httperr.WithCode(errors.New("ticket id missing"), http.StatusBadRequest)
	}

	var request struct {
		Names []string  `json:"names"`
		PIN   model.PIN `json:"pin"`
	}
	err := jr.Decode(&request)
	if err != nil {
		return httperr.WithCode(err, http.StatusBadRequest)
	}

	request.Names = trimEmptyRight(request.Names)

	var success bool
	err = t.back.SetNamesWithPIN(model.ID(idS), request.Names, request.PIN)
	switch err {
	case nil:
		t.l.Debug("set names",
			log.String("id", idS),
			log.String("pin", string(request.PIN)),
			log.Strings("names", request.Names),
		)
		success = true
	case backend.ErrUnauthorized:
		t.l.Debug("unauthorized")
		success = false
	default:
		t.l.Debug("got error",
			log.Error(err),
			log.String("type", fmt.Sprintf("%T", err)),
		)
		return err
	}

	jw.Encode(struct {
		Success bool `json:"success"`
	}{
		success,
	})

	return nil
}

func (t tickets) SetNames(w http.ResponseWriter, r *http.Request) error {
	jw, jr := httpjson.Wrap(w, r)
	t.l.Debug("running SetNames")

	idS, ok := mux.Vars(r)["id"]
	if !ok {
		t.l.Debug("got request without ticket id")
		return httperr.WithCode(errors.New("ticket id missing"), http.StatusBadRequest)
	}

	var request struct {
		Names []string `json:"names"`
	}
	err := jr.Decode(&request)
	if err != nil {
		return httperr.WithCode(err, http.StatusBadRequest)
	}
	request.Names = trimEmptyRight(request.Names)

	err = t.back.SetNames(model.ID(idS), request.Names)
	if err != nil {
		return err
	}

	jw.WriteHeader(http.StatusNoContent)
	return nil
}

func trimEmptyRight(s []string) []string {
	l := len(s) - 1
	for l >= 0 && s[l] == "" {
		l--
	}
	return s[:l+1]
}
