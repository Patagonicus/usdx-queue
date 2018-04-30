package main

import (
	"context"
	"crypto/sha512"
	"errors"
	"flag"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/Patagonicus/group"
	"github.com/Patagonicus/usdx-queue/pkg/api"
	"github.com/Patagonicus/usdx-queue/pkg/auth"
	"github.com/Patagonicus/usdx-queue/pkg/backend"
	"github.com/Patagonicus/usdx-queue/pkg/log"
	"github.com/Patagonicus/usdx-reader/pkg/storage"
	"github.com/Patagonicus/usdx-reader/pkg/storage/mysql"
	bolt "github.com/coreos/bbolt"
	"github.com/gorilla/mux"
	cache "github.com/patrickmn/go-cache"
)

var errInterrupted = errors.New("interrupted")

func main() {
	var (
		listen      = flag.String("listen", ":8080", "listen address for the API")
		dbPath      = flag.String("db", "usdx.db", "path to the database")
		authDBPath  = flag.String("auth-db", "auth.db", "path to the database")
		songs       = flag.String("songs", "", "connect string for mysql")
		coverPath   = flag.String("cover-path", "", "")
		createAdmin = flag.String("create-admin", "", "creates a new admin token with the given name")
	)
	flag.Parse()

	//	l := log.NewProduction()
	l := log.NewDevelopment()
	defer l.Sync()

	l.Debug("parsed flags",
		log.String("listen", *listen),
		log.String("db", *dbPath),
		log.String("auth-db", *authDBPath),
		log.String("songs", *songs),
		log.String("create-admin", *createAdmin),
	)

	backendDB, err := openDB(*dbPath)
	if err != nil {
		l.Error("failed to open backend database",
			log.String("path", *dbPath),
			log.Error(err),
		)
		return
	}
	defer backendDB.Close()

	authDB, err := openDB(*authDBPath)
	if err != nil {
		l.Error("failed to open auth database",
			log.String("path", *authDBPath),
			log.Error(err),
		)
		return
	}
	defer authDB.Close()

	backend, err := createBackend(l.Named("backend"), backendDB)
	if err != nil {
		l.Error("failed to create backend",
			log.Error(err),
		)
		return
	}

	authenticator, err := createAuth(l.Named("auth"), authDB)
	if err != nil {
		l.Error("failed to create authentication backend",
			log.Error(err),
		)
		return
	}

	if len(*createAdmin) > 0 {
		c, err := authenticator.CreateClient(*createAdmin, auth.TypeAdmin)
		if err != nil {
			l.Error("failed to create admin token",
				log.Error(err),
			)
			return
		}
		l.Info("created admin token",
			log.String("name", *createAdmin),
			log.String("token", string(c.GetToken())),
		)
	}

	storage, err := mysql.OpenExisting(*songs)
	if err != nil {
		l.Error("failed to open mysql",
			log.Error(err),
		)
		return
	}

	coverLoader := coverLoader{
		base:  *coverPath,
		cache: cache.New(5*time.Minute, 10*time.Minute),
	}

	err = group.Run(
		createServerActor(l.Named("server"), *listen, authenticator, backend, storage, coverLoader),
		createInterruptActor(l.Named("interrupt")),
	)
	if err != nil && err != errInterrupted {
		l.Error("error running server",
			log.Error(err),
		)
	}
}

func openDB(path string) (*bolt.DB, error) {
	return bolt.Open(path, 0600, nil)
}

func createBackend(l log.Logger, db *bolt.DB) (*backend.Backend, error) {
	return backend.New(l, db)
}

func createAuth(l log.Logger, db *bolt.DB) (auth.Authenticator, error) {
	return auth.New(db)
}

func createServerActor(l log.Logger, listen string, a auth.Authenticator, back *backend.Backend, storage storage.Backend, coverLoader coverLoader) group.Actor {
	apiV1, err := api.New(l, a, back, storage, coverLoader)
	if err != nil {
		return group.Done(err)
	}

	handler := mux.NewRouter()
	handler.PathPrefix("/v1/").Handler(http.StripPrefix("/v1", apiV1))

	stdLog, err := l.NewStdLogAt(log.WarnLevel)
	if err != nil {
		return group.Done(err)
	}

	server := &http.Server{
		Addr:         listen,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  30 * time.Second,
		ErrorLog:     stdLog,
	}

	return group.New(
		func() error {
			return server.ListenAndServe()
		},
		func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			err := server.Shutdown(ctx)
			if err != nil {
				l.Error("failed to shut down server",
					log.Error(err),
				)
			}
		},
	)
}

func createInterruptActor(l log.Logger) group.Actor {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	return group.WithChannel(func(done <-chan struct{}) error {
		select {
		case <-c:
			return errInterrupted
		case <-done:
			return nil
		}
	})
}

type coverLoader struct {
	base  string
	cache *cache.Cache
}

type entry struct {
	data []byte
	etag []byte
	err  error
}

func (c coverLoader) Get(path string) ([]byte, []byte, error) {
	e, ok := c.cache.Get(path)
	if ok {
		ent := e.(entry)
		return ent.data, ent.etag, ent.err
	}

	data, err := ioutil.ReadFile(filepath.Join(c.base, path))
	if err != nil {
		c.cache.Add(path, entry{nil, nil, err}, 0)
		return nil, nil, err
	}
	etag := sha512.Sum512(data)
	c.cache.Set(path, entry{data, etag[:], nil}, 0)
	return data, etag[:], nil
}
