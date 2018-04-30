package backend

import "github.com/Patagonicus/usdx-queue/pkg/model"

type ticket model.Ticket

func (t ticket) Ticket() model.Ticket {
	return model.Ticket(t)
}

type id model.ID

func idFromKey(key []byte) id {
	return id(key)
}

func (id id) Key() []byte {
	return []byte(id)
}

func (id id) ID() model.ID {
	return model.ID(id)
}

type version = model.Version
type pin = model.PIN

var dontCare = model.DontCare

type queue struct {
	Queue   []id
	Pos     int
	Paused  bool
	Version version
}

func (q queue) ModelQueue() model.Queue {
	ids := make([]model.ID, len(q.Queue))
	for i, id := range q.Queue {
		ids[i] = model.ID(id)
	}

	return model.Queue{
		ids,
		q.Pos,
		q.Paused,
		q.Version,
	}
}
