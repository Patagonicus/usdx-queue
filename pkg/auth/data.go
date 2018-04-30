package auth

type token Token

func tokenFromKey(k []byte) token {
	return token(k)
}

func fromToken(t Token) token {
	return token(t)
}

func (t token) Token() Token {
	return Token(t)
}

func (t token) Key() []byte {
	return []byte(t)
}

type client struct {
	Name  string
	Typ   PermType
	Token token
}

func fromClient(c Client) client {
	return client{
		c.name,
		c.typ,
		fromToken(c.token),
	}
}

func (c client) Client() Client {
	return Client{
		c.Name,
		c.Typ,
		c.Token.Token(),
	}
}
