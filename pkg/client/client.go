package client

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"path"
	"time"

	"github.com/Patagonicus/usdx-queue/pkg/auth"
	"github.com/Patagonicus/usdx-queue/pkg/log"
	"github.com/Patagonicus/usdx-queue/pkg/model"
)

var ErrPINInvalid = errors.New("PIN invalid")

type Client struct {
	c          *http.Client
	address    *url.URL
	pubAddress *url.URL
	headers    map[string][]string
	l          log.Logger
}

func New(l log.Logger, address *url.URL, token auth.Token) Client {
	return NewWithPub(l, address, address, token)
}

func NewWithPub(l log.Logger, address, pub *url.URL, token auth.Token) Client {
	auth := base64.StdEncoding.EncodeToString([]byte(":" + string(token)))

	return Client{
		c: &http.Client{
			Timeout: 3 * time.Second,
		},
		address:    address,
		pubAddress: pub,
		headers: map[string][]string{
			"Authorization": []string{"Basic " + auth},
		},
		l: l,
	}
}

func (c Client) do(req *http.Request) (status, map[string][]string, body, error) {
	c.l.Debug("executing request",
		log.Stringer("request", req.URL),
	)
	return wrap(c.c.Do(req))
}

func (c Client) get(url *url.URL, header map[string][]string) (status, map[string][]string, body, error) {
	req, err := http.NewRequest("GET", url.String(), nil)
	if err != nil {
		return status{}, nil, body{}, err
	}
	req.Header = header
	return c.do(req)
}

func (c Client) post(url *url.URL, header map[string][]string, b io.Reader) (status, map[string][]string, body, error) {
	req, err := http.NewRequest("POST", url.String(), b)
	if err != nil {
		return status{}, nil, body{}, err
	}
	req.Header = header
	return c.do(req)
}

func (c Client) patch(url *url.URL, header map[string][]string, b io.Reader) (status, map[string][]string, body, error) {
	req, err := http.NewRequest("PATCH", url.String(), b)
	if err != nil {
		return status{}, nil, body{}, err
	}
	req.Header = header
	return c.do(req)
}

func (c Client) getURL(p string) *url.URL {
	url := new(url.URL)
	*url = *c.address
	url.Path = path.Join(url.Path, p)
	return url
}

func (c Client) CreateTicket() (model.ID, model.PIN, error) {
	status, headers, body, err := c.post(c.getURL("/v1/tickets"), c.headers, nil)
	if err != nil {
		return model.ID(""), model.PIN(""), err
	}
	defer body.Close()

	if !status.IsSuccess() {
		return model.ID(""), model.PIN(""), fmt.Errorf("no success creating ticket: %d, %s", status.Code, status.Reason)
	}

	var response struct {
		PIN model.PIN
	}
	err = json.NewDecoder(body).Decode(&response)
	if err != nil {
		return model.ID(""), model.PIN(""), err
	}

	location, ok := headers["Location"]
	if !ok || len(location) != 1 {
		return model.ID(""), model.PIN(""), fmt.Errorf("error getting ID of new ticket")
	}

	return model.ID(location[0]), response.PIN, nil
}

func (c Client) GetQueue() (model.Queue, error) {
	status, _, body, err := c.get(c.getURL("/v1/queue"), c.headers)
	c.l.Debug("got queue",
		log.Any("status", status),
		log.Error(err),
	)
	if err != nil {
		return model.Queue{}, err
	}
	defer body.Close()

	if !status.IsSuccess() {
		return model.Queue{}, fmt.Errorf("could not get queue: %d, %s", status.Code, status.Reason)
	}

	var q model.Queue
	err = json.NewDecoder(body).Decode(&q)
	return q, err
}

func (c Client) GetTicket(id model.ID) (model.Ticket, error) {
	url := c.getURL("/v1/tickets/" + url.PathEscape(string(id)))
	status, _, body, err := c.get(url, c.headers)
	if err != nil {
		return model.Ticket{}, err
	}
	defer body.Close()

	if !status.IsSuccess() {
		return model.Ticket{}, fmt.Errorf("could not get ticket: %d, %s", status.Code, status.Reason)
	}

	var ticket model.Ticket
	err = json.NewDecoder(body).Decode(&ticket)
	return ticket, err
}

func (c Client) GetTickets() ([]model.Ticket, error) {
	status, _, body, err := c.get(c.getURL("/v1/tickets"), c.headers)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	if !status.IsSuccess() {
		return nil, fmt.Errorf("could not get tickets: %d, %s", status.Code, status.Reason)
	}

	var tickets []model.Ticket
	err = json.NewDecoder(body).Decode(&tickets)
	return tickets, err
}

func (c Client) GetState() (model.State, error) {
	status, _, body, err := c.get(c.getURL("/v1/state"), c.headers)
	if err != nil {
		return model.State{}, err
	}
	defer body.Close()

	if !status.IsSuccess() {
		return model.State{}, fmt.Errorf("could not get state: %d, %s", status.Code, status.Reason)
	}

	var state model.State
	err = json.NewDecoder(body).Decode(&state)
	return state, err
}

func (c Client) SetState(state model.State) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}

	status, _, body, err := c.post(c.getURL("/v1/state"), c.headers, bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer body.Close()

	if !status.IsSuccess() {
		return fmt.Errorf("could not set state: %d, %s", status.Code, status.Reason)
	}
	return nil
}

func (c Client) Advance() error {
	status, _, body, err := c.post(c.getURL("/v1/queue/actions/advance"), c.headers, nil)
	if err != nil {
		return err
	}
	defer body.Close()

	if !status.IsSuccess() {
		return fmt.Errorf("could not advance: %d, %s", status.Code, status.Reason)
	}
	return nil
}

func (c Client) GoBack() error {
	status, _, body, err := c.post(c.getURL("/v1/queue/actions/goback"), c.headers, nil)
	if err != nil {
		return err
	}
	defer body.Close()

	if !status.IsSuccess() {
		return fmt.Errorf("could not go back: %d, %s", status.Code, status.Reason)
	}
	return nil
}

func (c Client) TogglePause() error {
	status, _, body, err := c.post(c.getURL("/v1/queue/actions/pause"), c.headers, nil)
	if err != nil {
		return err
	}
	defer body.Close()

	if !status.IsSuccess() {
		return fmt.Errorf("could not toggle pause: %d, %s", status.Code, status.Reason)
	}
	return nil
}

func (c Client) GetCoverURL(source string) string {
	return fmt.Sprintf("%s/v1/songs/%s/cover", c.pubAddress, base64.StdEncoding.EncodeToString([]byte(source)))
}

func (c Client) GetSongs() ([]model.Song, error) {
	status, _, body, err := c.get(c.getURL("/v1/songs"), c.headers)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	if !status.IsSuccess() {
		return nil, fmt.Errorf("could not get songs: %d, %s", status.Code, status.Reason)
	}

	var songs []model.Song
	err = json.NewDecoder(body).Decode(&songs)
	return songs, err
}

func (c Client) GetSong(source string) (model.Song, error) {
	url := c.getURL("/v1/songs/" + url.PathEscape(base64.StdEncoding.EncodeToString([]byte(source))))
	status, _, body, err := c.get(url, c.headers)
	if err != nil {
		return model.Song{}, err
	}
	defer body.Close()

	if !status.IsSuccess() {
		return model.Song{}, fmt.Errorf("could not get song: %d, %s", status.Code, status.Reason)
	}

	var song model.Song
	err = json.NewDecoder(body).Decode(&song)
	return song, err
}

func (c Client) SetNames(id model.ID, pin model.PIN, names []string) error {
	data, err := json.Marshal(struct {
		Names []string `json:"names"`
		PIN   string   `json:"pin"`
	}{
		Names: names,
		PIN:   string(pin),
	})
	if err != nil {
		return err
	}

	url := c.getURL("/v1/tickets/" + url.PathEscape(string(id)))
	status, _, body, err := c.patch(url, c.headers, bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer body.Close()

	if !status.IsSuccess() {
		return fmt.Errorf("failed to set names: %d, %s", status.Code, status.Reason)
	}

	var result struct {
		Success bool `json:"success"`
	}
	err = json.NewDecoder(body).Decode(&result)
	if err != nil {
		return err
	}

	if !result.Success {
		return ErrPINInvalid
	}

	return nil
}

type status struct {
	Code   int
	Reason string
}

func (s status) IsSuccess() bool {
	return s.Code >= 200 && s.Code <= 299
}

type body struct {
	io.ReadCloser
}

func (b body) Close() error {
	_, err := ioutil.ReadAll(b)
	if err != nil {
		b.Close()
		return err
	}
	return b.ReadCloser.Close()
}

func wrap(r *http.Response, err error) (status, map[string][]string, body, error) {
	if err != nil {
		return status{}, nil, body{}, err
	}
	return status{
			Code:   r.StatusCode,
			Reason: r.Status,
		},
		r.Header,
		body{r.Body},
		nil
}
