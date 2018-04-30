package backend

import (
	"bytes"
	"encoding/gob"
	"fmt"

	bolt "github.com/coreos/bbolt"
)

var (
	queueBucket   = []byte("queue")
	ticketsBucket = []byte("tickets")
	pinsBucket    = []byte("pins")
)

var allBuckets = [][]byte{
	queueBucket,
	ticketsBucket,
	pinsBucket,
}

var queueKey = []byte("queue")

type db struct {
	db *bolt.DB
}

func (d db) Init() error {
	err := d.checkBuckets()
	if err != nil {
		return err
	}

	return d.checkQueue()
}

func (d db) checkBuckets() error {
	err := d.View(func(t tx) error {
		for _, bucket := range allBuckets {
			_, err := t.bucket(bucket)
			if err != nil {
				return err
			}
		}
		return nil
	})

	if _, ok := err.(errBucketNotFound); !ok {
		return err
	}

	return d.Update(func(t tx) error {
		for _, bucket := range allBuckets {
			err := t.createBucketIfNotExist(bucket)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func (d db) checkQueue() error {
	err := d.View(func(t tx) error {
		_, err := t.GetQueue()
		return err
	})

	if _, ok := err.(errKeyNotFound); !ok {
		return nil
	}

	return d.Update(func(t tx) error {
		return t.PutQueue(queue{})
	})
}

func (d db) View(f func(tx) error) error {
	return d.db.View(func(btx *bolt.Tx) error {
		return f(tx{btx})
	})
}

func (d db) Update(f func(tx) error) error {
	return d.db.Update(func(btx *bolt.Tx) error {
		return f(tx{btx})
	})
}

type tx struct {
	tx *bolt.Tx
}

func (t tx) bucket(key []byte) (*bolt.Bucket, error) {
	bucket := t.tx.Bucket(key)
	if bucket == nil {
		return nil, errBucketNotFound{key}
	}
	return bucket, nil
}

func (t tx) createBucketIfNotExist(key []byte) error {
	_, err := t.tx.CreateBucketIfNotExists(key)
	return err
}

func (t tx) get(bucket, key []byte) ([]byte, error) {
	b, err := t.bucket(bucket)
	if err != nil {
		return nil, err
	}

	data := b.Get(key)
	if data == nil {
		return nil, errKeyNotFound{key}
	}

	return data, nil
}

func (t tx) put(bucket, key, data []byte) error {
	b, err := t.bucket(bucket)
	if err != nil {
		return err
	}

	return b.Put(key, data)
}

func (t tx) forEach(bucket []byte, f func(k, v []byte) error) error {
	b, err := t.bucket(bucket)
	switch err.(type) {
	case nil:
		return b.ForEach(f)
	case errBucketNotFound:
		return nil
	default:
		return err
	}
}

func (t tx) NextTicketSequence() (uint64, error) {
	bucket, err := t.bucket(ticketsBucket)
	if err != nil {
		return 0, err
	}

	return bucket.NextSequence()
}

func (t tx) GetTickets() (map[id]ticket, error) {
	result := make(map[id]ticket)

	err := t.forEach(ticketsBucket, func(k, v []byte) error {
		ticket, err := decodeTicket(v)
		if err != nil {
			return err
		}
		result[idFromKey(k)] = ticket
		return nil
	})

	return result, err
}

func (t tx) GetTicket(id id) (ticket, error) {
	data, err := t.get(ticketsBucket, id.Key())
	if err != nil {
		return ticket{}, err
	}

	return decodeTicket(data)
}

func (t tx) PutTicket(ticket ticket) error {
	data, err := encode(ticket)
	if err != nil {
		return err
	}

	return t.put(ticketsBucket, id(ticket.ID).Key(), data)
}

func (t tx) GetPINs() (map[id]pin, error) {
	result := make(map[id]pin)

	err := t.forEach(pinsBucket, func(k, v []byte) error {
		pin, err := decodePin(v)
		if err != nil {
			return err
		}
		result[idFromKey(k)] = pin
		return nil
	})

	return result, err
}

func (t tx) GetPIN(id id) (pin, error) {
	data, err := t.get(pinsBucket, id.Key())
	if err != nil {
		return pin(""), err
	}

	return decodePin(data)
}

func (t tx) PutPIN(id id, pin pin) error {
	data, err := encode(pin)
	if err != nil {
		return err
	}

	return t.put(pinsBucket, id.Key(), data)
}

func (t tx) GetQueue() (queue, error) {
	data, err := t.get(queueBucket, queueKey)
	if err != nil {
		return queue{}, err
	}

	return decodeQueue(data)
}

func (t tx) PutQueue(queue queue) error {
	data, err := encode(queue)
	if err != nil {
		return err
	}

	return t.put(queueBucket, queueKey, data)
}

func decodeTicket(data []byte) (ticket, error) {
	var ticket ticket
	err := decode(data, &ticket)
	return ticket, err
}

func decodePin(data []byte) (pin, error) {
	var pin pin
	err := decode(data, &pin)
	return pin, err
}

func decodeQueue(data []byte) (queue, error) {
	var queue queue
	err := decode(data, &queue)
	return queue, err
}

func decode(data []byte, v interface{}) error {
	return gob.NewDecoder(bytes.NewReader(data)).Decode(v)
}

func encode(v interface{}) ([]byte, error) {
	buf := new(bytes.Buffer)
	err := gob.NewEncoder(buf).Encode(v)
	return buf.Bytes(), err
}

type errBucketNotFound struct {
	Bucket []byte
}

func (e errBucketNotFound) Error() string {
	return fmt.Sprintf("bucket %v not found", e.Bucket)
}

type errKeyNotFound struct {
	Key []byte
}

func (e errKeyNotFound) Error() string {
	return fmt.Sprintf("key %v not found", e.Key)
}
