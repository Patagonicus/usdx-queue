package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/Patagonicus/usdx-queue/pkg/auth"
	"github.com/Patagonicus/usdx-queue/pkg/httperr"
	"github.com/Patagonicus/usdx-queue/pkg/log"
	httpauth "github.com/Patagonicus/usdx-queue/pkg/middleware/auth"
	"github.com/Patagonicus/usdx-queue/pkg/middleware/httpjson"
	"github.com/gorilla/mux"
)

type clients struct {
	authenticator auth.Authenticator
	l             log.Logger
}

func NewClients(l log.Logger, authenticator auth.Authenticator, router router) {
	requireList := httpauth.Require(l, authenticator, auth.PermListClients)
	requireCreate := httpauth.Require(l, authenticator, auth.PermCreateClient)
	requireDelete := httpauth.Require(l, authenticator, auth.PermDeleteClient)

	c := clients{
		authenticator: authenticator,
		l:             l,
	}

	router.Handle("/", requireList(httperr.HandlerFunc(c.List))).Methods("GET")
	router.Handle("/", requireCreate(httperr.HandlerFunc(c.Create))).Methods("POST")
	router.Handle("/{token}", requireList(httperr.HandlerFunc(c.Get))).Methods("GET")
	router.Handle("/{token}", requireDelete(httperr.HandlerFunc(c.Delete))).Methods("DELETE")
}

func (c clients) List(w http.ResponseWriter, r *http.Request) error {
	jw, _ := httpjson.Wrap(w, r)

	cs, err := c.authenticator.GetAll()
	if err != nil {
		c.l.Warn("failed to get all clients",
			log.Error(err),
		)
		return err
	}

	return jw.Encode(jsonClients(cs))
}

func (c clients) Get(w http.ResponseWriter, r *http.Request) error {
	jw, _ := httpjson.Wrap(w, r)

	tokenS, ok := mux.Vars(r)["token"]
	if !ok {
		c.l.Warn("got request without token argument")
		return httperr.WithCode(errors.New("missing argument: token"), http.StatusBadRequest)
	}

	c.l.Debug("looking up client",
		log.String("token", tokenS),
	)

	client, err := c.authenticator.Get(auth.Token(tokenS))
	switch err.(type) {
	case nil:
	case auth.ErrNotFound:
		return httperr.WithCode(err, http.StatusNotFound)
	default:
		return err
	}

	return jw.Encode(jsonClient{client})
}

func (c clients) Create(w http.ResponseWriter, r *http.Request) error {
	jw, jr := httpjson.Wrap(w, r)

	var request struct {
		Name string         `json:"name"`
		Type *auth.PermType `json:"type"`
	}
	err := jr.Decode(&request)
	if err != nil {
		return httperr.WithCode(err, http.StatusBadRequest)
	}

	if request.Type == nil {
		return httperr.WithCode(errors.New("missing type"), http.StatusBadRequest)
	}

	c.l.Debug("creating new client",
		log.Any("request", request),
	)
	client, err := c.authenticator.CreateClient(request.Name, *request.Type)
	if err != nil {
		return err
	}

	jw.Header().Set("Location", fmt.Sprintf("%s", client.GetToken()))
	jw.WriteHeader(http.StatusCreated)
	return nil
}

func (c clients) Delete(w http.ResponseWriter, r *http.Request) error {
	tokenS, ok := mux.Vars(r)["token"]
	if !ok {
		c.l.Warn("got request without token argument")
		return httperr.WithCode(errors.New("missing argument: token"), http.StatusBadRequest)
	}

	err := c.authenticator.Delete(auth.Token(tokenS))
	switch err.(type) {
	case nil:
	case auth.ErrNotFound:
		return httperr.WithCode(err, http.StatusNotFound)
	default:
		return err
	}

	w.WriteHeader(http.StatusNoContent)
	return nil
}

func jsonClients(clients []auth.Client) []jsonClient {
	result := make([]jsonClient, len(clients))
	for i, c := range clients {
		result[i] = jsonClient{c}
	}
	return result
}

type jsonClient struct {
	auth.Client
}

func (c jsonClient) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name  string `json:"name"`
		Typ   string `json:"type"`
		Token string `json:"token"`
	}{
		Name:  c.GetName(),
		Typ:   c.GetType().Name(),
		Token: string(c.GetToken()),
	})
}
