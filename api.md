`/clients`
* GET: list clients
* POST: create new client. Body must have the form `{"name":"some name","type":"some type"}` where "some type" is one of admin, advance, registration. Location header will contain Token of new client.

`/clients/{ID}`
* GET
* DELETE

`/tickets`
* GET: list tickets
* POST: create ticket. Location header has the id, body contains `{"pin":"0000"}`.

`/tickets/{ID}`
* GET
* PATCH: send `{"names":["foo","bar"]}` to set the names.
