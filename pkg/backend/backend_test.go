package backend_test

import (
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Patagonicus/usdx-queue/pkg/backend"
	"github.com/Patagonicus/usdx-queue/pkg/log"
	"github.com/Patagonicus/usdx-queue/pkg/model"
	bolt "github.com/coreos/bbolt"
)

func TestGetTicket(t *testing.T) {
	b, teardown := setupDB(t)
	defer teardown()

	ticket, _, err := b.CreateTicket()
	if err != nil {
		t.Fatalf("failed to create ticket: %s", err)
	}

	result, err := b.GetTicket(ticket.ID)
	if err != nil {
		t.Fatalf("failed to get ticket: %s", err)
	}
	if !reflect.DeepEqual(ticket, result) {
		t.Fatalf("expected %s, but got %s", ticket, result)
	}
}

func TestGetMultipleTickets(t *testing.T) {
	b, teardown := setupDB(t)
	defer teardown()

	var err error

	var tickets [5]model.Ticket
	for i := 0; i < len(tickets); i++ {
		tickets[i], _, err = b.CreateTicket()
		if err != nil {
			t.Fatalf("failed to create ticket %d: %s", i, err)
		}
	}

	for i, ticket := range tickets {
		result, err := b.GetTicket(ticket.ID)
		if err != nil {
			t.Fatalf("error getting ticket %d: %s", i, err)
		}
		if !reflect.DeepEqual(ticket, result) {
			t.Fatalf("wrong result for ticket %d: expected %s, but got %s", i, ticket, result)
		}
	}
}

func TestIDsDifferent(t *testing.T) {
	b, teardown := setupDB(t)
	defer teardown()

	ids := make(map[model.ID]struct{})
	for i := 0; i < 1000; i++ {
		ticket, _, err := b.CreateTicket()
		if err != nil {
			t.Fatalf("error creating ticket: %s", err)
		}
		if _, ok := ids[ticket.ID]; ok {
			t.Fatalf("duplicate id: %s", ticket.ID)
		}
		ids[ticket.ID] = struct{}{}
	}
}

func BenchmarkCreateTicket(b *testing.B) {
	back, teardown := setupDB(b)
	defer teardown()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		back.CreateTicket()
	}
}

func BenchmarkCreateTicketParallel(b *testing.B) {
	back, teardown := setupDB(b)
	defer teardown()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			back.CreateTicket()
		}
	})
}

func BenchmarkGetTicket(b *testing.B) {
	for _, n := range []int{1, 10, 100, 1000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			back, teardown := setupDB(b)
			defer teardown()

			var ids []model.ID

			for i := 0; i < n; i++ {
				ticket, err := back.CreateTicket()
				if err != nil {
					b.Fatalf("failed to create ticket: %s", err)
				}
				ids = append(ids, ticket.ID)
			}

			seed := time.Now().UnixNano()
			r := rand.New(rand.NewSource(seed))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				back.GetTicket(ids[r.Intn(len(ids))])
			}
		})
	}
}

func seeder(seeds chan<- int64, done <-chan struct{}) {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for {
		select {
		case seeds <- r.Int63():
		case <-done:
			close(seeds)
			return
		}
	}
}

func BenchmarkGetTicketParallel(b *testing.B) {
	for _, n := range []int{1, 10, 100, 1000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			back, teardown := setupDB(b)
			defer teardown()

			var ids []model.ID

			for i := 0; i < n; i++ {
				ticket, err := back.CreateTicket()
				if err != nil {
					b.Fatalf("failed to create ticket: %s", err)
				}
				ids = append(ids, ticket.ID)
			}

			seeds := make(chan int64)
			done := make(chan struct{})

			go seeder(seeds, done)

			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				r := rand.New(rand.NewSource(<-seeds))
				for pb.Next() {
					back.GetTicket(ids[r.Intn(len(ids))])
				}
			})
			b.StopTimer()

			close(done)
		})
	}
}

func setupDB(tb testing.TB) (*backend.Backend, func()) {
	dir, err := ioutil.TempDir("", "usdx-queue-test")
	if err != nil {
		tb.Fatalf("failed to create temp dir: %s", err)
	}

	dbPath := filepath.Join(dir, "test.db")

	db, err := bolt.Open(dbPath, 0600, &bolt.Options{
		NoGrowSync: true,
	})
	if err != nil {
		tb.Fatalf("failed to open db: %s", err)
	}
	db.NoSync = true

	logger := log.NullLogger

	if testing.Verbose() {
		logger = log.NewDevelopment()
	}

	b, err := backend.New(logger, db)
	if err != nil {
		tb.Fatalf("failed to create backend: %s", err)
	}

	return b, func() {
		err := db.Close()
		if err != nil {
			tb.Errorf("error closing bolt db: %v", err)
		}

		err = os.Remove(dbPath)
		if err != nil {
			tb.Errorf("error removing db: %v", err)
		}

		err = os.Remove(dir)
		if err != nil {
			tb.Errorf("error removing temp dir: %v", err)
		}

		logger.Sync()
	}
}
