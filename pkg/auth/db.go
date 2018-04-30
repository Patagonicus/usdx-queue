package auth

import (
	"bytes"
	"encoding/gob"
	"fmt"

	bolt "github.com/coreos/bbolt"
)

var (
	clientsBucket = []byte("clients")
)

var allBuckets = [][]byte{
	clientsBucket,
}

type db struct {
	db *bolt.DB
}

func (d db) Init() error {
	return d.checkBuckets()
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

func (t tx) del(bucket, key []byte) error {
	b, err := t.bucket(bucket)
	if err != nil {
		return err
	}

	if b.Get(key) == nil {
		return errKeyNotFound{key}
	}

	return b.Delete(key)
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

func (t tx) GetClients() (map[token]client, error) {
	result := make(map[token]client)

	err := t.forEach(clientsBucket, func(k, v []byte) error {
		client, err := decodeClient(v)
		if err != nil {
			return err
		}
		result[tokenFromKey(k)] = client
		return nil
	})

	return result, err
}

func (t tx) GetClient(token token) (client, error) {
	data, err := t.get(clientsBucket, token.Key())
	if err != nil {
		return client{}, err
	}

	return decodeClient(data)
}

func (t tx) PutClient(c client) error {
	data, err := encode(c)
	if err != nil {
		return err
	}
	return t.put(clientsBucket, c.Token.Key(), data)
}

func (t tx) DeleteClient(token token) error {
	return t.del(clientsBucket, token.Key())
}

func decodeClient(data []byte) (client, error) {
	var client client
	err := decode(data, &client)
	return client, err
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
