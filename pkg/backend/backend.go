package backend

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"sync/atomic"

	"github.com/Patagonicus/usdx-queue/pkg/log"
	"github.com/Patagonicus/usdx-queue/pkg/model"
	bolt "github.com/coreos/bbolt"
)

type ErrTicketDoesNotExist struct {
	ID model.ID
}

var maxPIN = big.NewInt(10000)

var (
	ErrUnauthorized         = errors.New("unauthorized")
	ErrInvalidQueueMovement = errors.New("invalid queue movement")
)

func (e ErrTicketDoesNotExist) Error() string {
	return fmt.Sprintf("ticket %s does not exist", e.ID)
}

type Backend struct {
	currentState atomic.Value
	db           db
	l            log.Logger
}

func New(l log.Logger, boltDB *bolt.DB) (*Backend, error) {
	d := db{boltDB}

	err := d.Init()
	if err != nil {
		return nil, err
	}

	b := &Backend{
		db: d,
		l:  l,
	}
	b.setState(model.State{})
	return b, nil
}

func (b *Backend) Close() error {
	return nil
}

func (b *Backend) setState(s model.State) {
	b.currentState.Store(s)
}

func (b *Backend) getState() model.State {
	return b.currentState.Load().(model.State)
}

func (b *Backend) UpdateState(s model.State) error {
	b.l.Debug("setting state",
		log.Any("state", s),
	)
	b.setState(s)
	return nil
}

func (b *Backend) GetState() (model.State, error) {
	return b.getState(), nil
}

func (b *Backend) CreateTicket() (model.Ticket, model.PIN, error) {
	var ticket ticket
	p, err := rand.Int(rand.Reader, maxPIN)
	if err != nil {
		return model.Ticket{}, model.PIN(""), err
	}
	var pinS = pin(fmt.Sprintf("%04d", p))

	err = b.db.Update(func(t tx) error {
		idNum, err := t.NextTicketSequence()
		if err != nil {
			return err
		}

		ticketID := id(strconv.FormatUint(idNum, 10))
		ticket.ID = ticketID.ID()

		err = t.PutTicket(ticket)
		if err != nil {
			return err
		}

		err = t.PutPIN(ticketID, pinS)
		if err != nil {
			return err
		}

		queue, err := t.GetQueue()
		if err != nil {
			return err
		}

		queue.Queue = append(queue.Queue, ticketID)
		queue.Version++

		return t.PutQueue(queue)
	})

	return ticket.Ticket(), model.PIN(pinS), err
}

func (b *Backend) GetTicket(ticketID model.ID) (model.Ticket, error) {
	var ticket ticket

	err := b.db.View(func(t tx) error {
		var err error
		ticket, err = t.GetTicket(id(ticketID))
		return err
	})
	switch err.(type) {
	case nil:
	case errKeyNotFound:
		return model.Ticket{}, ErrTicketDoesNotExist{ticketID}
	default:
		return model.Ticket{}, err
	}
	return ticket.Ticket(), nil
}

func (b *Backend) GetTickets() (map[model.ID]model.Ticket, error) {
	result := make(map[model.ID]model.Ticket)
	var tickets map[id]ticket

	err := b.db.View(func(t tx) error {
		var err error
		tickets, err = t.GetTickets()
		return err
	})
	if err != nil {
		return result, err
	}

	for k, v := range tickets {
		result[k.ID()] = v.Ticket()
	}

	return result, nil
}

func (b *Backend) SetNames(ticketID model.ID, names []string) error {
	err := b.db.Update(func(t tx) error {
		ticket, err := t.GetTicket(id(ticketID))
		if err != nil {
			return err
		}

		ticket.Names = names

		return t.PutTicket(ticket)
	})
	if _, ok := err.(errKeyNotFound); ok {
		return ErrTicketDoesNotExist{ticketID}
	}
	return err
}

func (b *Backend) SetNamesWithPIN(ticketID model.ID, names []string, p model.PIN) error {
	err := b.db.Update(func(t tx) error {
		ticket, err := t.GetTicket(id(ticketID))
		if err != nil {
			return err
		}

		storedPIN, err := t.GetPIN(id(ticketID))
		if err != nil {
			return err
		}

		if storedPIN != p {
			return ErrUnauthorized
		}

		ticket.Names = names
		return t.PutTicket(ticket)
	})
	if _, ok := err.(errKeyNotFound); ok {
		return ErrTicketDoesNotExist{ticketID}
	}
	return err
}

func (b *Backend) GetQueue() (model.Queue, error) {
	var queue queue
	err := b.db.View(func(t tx) error {
		var err error
		queue, err = t.GetQueue()
		return err
	})
	if err != nil {
		return model.Queue{}, err
	}
	return queue.ModelQueue(), err
}

func (b *Backend) Advance() error {
	return b.db.Update(func(t tx) error {
		queue, err := t.GetQueue()
		if err != nil {
			return err
		}

		if queue.Paused || queue.Pos >= len(queue.Queue)-1 {
			return ErrInvalidQueueMovement
		}

		queue.Pos++
		return t.PutQueue(queue)
	})
}

func (b *Backend) GoBack() error {
	return b.db.Update(func(t tx) error {
		queue, err := t.GetQueue()
		if err != nil {
			return err
		}

		if queue.Paused || queue.Pos <= 0 {
			return ErrInvalidQueueMovement
		}

		queue.Pos--
		return t.PutQueue(queue)
	})
}

func (b *Backend) Pause() error {
	return b.db.Update(func(t tx) error {
		queue, err := t.GetQueue()
		if err != nil {
			return err
		}

		queue.Paused = !queue.Paused
		return t.PutQueue(queue)
	})
}
