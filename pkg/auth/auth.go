package auth

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"fmt"

	bolt "github.com/coreos/bbolt"
)

type ErrNotFound struct {
	token Token
}

func (e ErrNotFound) Error() string {
	return fmt.Sprintf("Token %v not found", e.token)
}

const tokenBytes = 32

type PermType int

const (
	TypeInvalid PermType = iota
	TypeAdmin
	TypeAdvancer
	TypeRegistration
	TypeWeb
	TypeBeamer
)

var permTypeNames = map[PermType]string{
	TypeAdmin:        "admin",
	TypeAdvancer:     "advancer",
	TypeRegistration: "registration",
	TypeWeb:          "web",
	TypeBeamer:       "beamer",
}

var byName = map[string]PermType{
	"admin":        TypeAdmin,
	"advancer":     TypeAdvancer,
	"registration": TypeRegistration,
	"web":          TypeWeb,
	"beamer":       TypeBeamer,
}

func FromName(name string) PermType {
	return byName[name]
}

func (p PermType) String() string {
	return fmt.Sprintf("PermType{%s}", p.Name())
}

func (p PermType) Name() string {
	name, ok := permTypeNames[p]
	if !ok {
		name = fmt.Sprintf("Invalid(%d)", p)
	}
	return name
}

func (p PermType) IsValid() bool {
	_, ok := permTypeNames[p]
	return ok
}

func (p PermType) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.Name())
}

func (p *PermType) UnmarshalJSON(data []byte) error {
	var name string
	err := json.Unmarshal(data, &name)
	if err != nil {
		return err
	}
	*p = FromName(name)
	return nil
}

var (
	PermListTickets Permission = permission{
		name:    "list tickets",
		allowed: []PermType{TypeAdmin, TypeWeb, TypeBeamer},
	}
	PermCreateTicket Permission = permission{
		name:    "create ticket",
		allowed: []PermType{TypeAdmin, TypeRegistration},
	}
	PermSetNamesWithPIN Permission = permission{
		name:    "set names with PIN",
		allowed: []PermType{TypeAdmin, TypeWeb},
	}
	PermSetNames Permission = permission{
		name:    "set names",
		allowed: []PermType{TypeAdmin},
	}

	PermListClients Permission = permission{
		name:    "list clients",
		allowed: []PermType{TypeAdmin},
	}
	PermCreateClient Permission = permission{
		name:    "create clients",
		allowed: []PermType{TypeAdmin},
	}
	PermDeleteClient Permission = permission{
		name:    "create clients",
		allowed: []PermType{TypeAdmin},
	}

	PermListQueue Permission = permission{
		name:    "list queue",
		allowed: []PermType{TypeAdmin, TypeWeb, TypeBeamer},
	}
	PermAdvanceQueue Permission = permission{
		name:    "advance queue",
		allowed: []PermType{TypeAdmin, TypeAdvancer, TypeWeb},
	}
	PermGoBackQueue Permission = permission{
		name:    "goback",
		allowed: []PermType{TypeAdmin, TypeWeb},
	}
	PermPauseQueue Permission = permission{
		name:    "pause",
		allowed: []PermType{TypeAdmin, TypeWeb},
	}

	PermListState Permission = permission{
		name:    "list state",
		allowed: []PermType{TypeAdmin, TypeWeb, TypeBeamer},
	}
	PermSetState Permission = permission{
		name:    "set state",
		allowed: []PermType{TypeAdmin, TypeAdvancer},
	}

	PermGetSong Permission = permission{
		name:    "get song",
		allowed: []PermType{TypeAdmin, TypeBeamer, TypeWeb},
	}
	PermListSong Permission = permission{
		name:    "list song",
		allowed: []PermType{TypeAdmin, TypeWeb},
	}
)

type Authenticator struct {
	db db
}

func New(boltDB *bolt.DB) (Authenticator, error) {
	db := db{boltDB}
	err := db.Init()
	return Authenticator{
		db: db,
	}, err
}

func (a Authenticator) CreateClient(name string, typ PermType) (Client, error) {
	if !typ.IsValid() {
		return Client{}, fmt.Errorf("invalid permission type: %s", typ)
	}

	token, err := createToken()
	if err != nil {
		return Client{}, err
	}

	c := Client{
		name:  name,
		typ:   typ,
		token: token,
	}

	dbC := fromClient(c)
	err = a.db.Update(func(t tx) error {
		return t.PutClient(dbC)
	})
	return c, err
}

func createToken() (Token, error) {
	data := make([]byte, tokenBytes)
	_, err := rand.Read(data)
	if err != nil {
		return Token(""), err
	}
	return Token(base32.StdEncoding.EncodeToString(data)), nil
}

func (a Authenticator) Get(t Token) (Client, error) {
	token := fromToken(t)
	var c client
	err := a.db.View(func(t tx) error {
		var err error
		c, err = t.GetClient(token)
		return err
	})
	if _, ok := err.(errKeyNotFound); ok {
		return c.Client(), ErrNotFound{t}
	}
	return c.Client(), err
}

func (a Authenticator) GetAll() ([]Client, error) {
	var clients map[token]client

	err := a.db.View(func(t tx) error {
		var err error
		clients, err = t.GetClients()
		return err
	})
	if err != nil {
		return nil, err
	}

	result := make([]Client, 0, len(clients))
	for _, c := range clients {
		result = append(result, c.Client())
	}

	return result, nil
}

func (a Authenticator) Delete(t Token) error {
	token := fromToken(t)
	err := a.db.Update(func(t tx) error {
		return t.DeleteClient(token)
	})
	if _, ok := err.(errKeyNotFound); ok {
		return ErrNotFound{t}
	}
	return err
}

type Permission interface {
	HasPermission(c Client) bool
	Name() string
}

type permission struct {
	name    string
	allowed []PermType
}

func (p permission) String() string {
	return fmt.Sprintf("Perm{%s, allowed: %s}", p.name, p.allowed)
}

func (p permission) Name() string {
	return p.name
}

func (p permission) HasPermission(c Client) bool {
	typ := c.GetType()
	for _, allowed := range p.allowed {
		if typ == allowed {
			return true
		}
	}
	return false
}

type Client struct {
	name  string
	typ   PermType
	token Token
}

func (c Client) GetName() string {
	return c.name
}

func (c Client) GetType() PermType {
	return c.typ
}

func (c Client) GetToken() Token {
	return c.token
}

func (c Client) String() string {
	return fmt.Sprintf("Client{%v %s %s}", c.name, c.typ, c.token)
}

type Token string
