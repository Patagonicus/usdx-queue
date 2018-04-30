package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"

	"github.com/Patagonicus/usdx-queue/pkg/auth"
	"github.com/Patagonicus/usdx-queue/pkg/httperr"
	"github.com/Patagonicus/usdx-queue/pkg/log"
	httpauth "github.com/Patagonicus/usdx-queue/pkg/middleware/auth"
	"github.com/Patagonicus/usdx-queue/pkg/middleware/httpjson"
	"github.com/Patagonicus/usdx-queue/pkg/model"
	"github.com/Patagonicus/usdx-reader/pkg/storage"
	"github.com/gorilla/mux"
)

var enc = base64.StdEncoding

type CoverLoader interface {
	Get(path string) ([]byte, []byte, error)
}

type songsAPI struct {
	allSongs string
	songs    map[string]model.Song
	covers   map[string]string
	cl       CoverLoader
	l        log.Logger
}

func NewSongs(l log.Logger, a auth.Authenticator, b storage.Backend, coverLoader CoverLoader, router router) error {
	requireGet := httpauth.Require(l, a, auth.PermGetSong)
	requireList := httpauth.Require(l, a, auth.PermListSong)
	s := &songsAPI{
		songs:  make(map[string]model.Song),
		covers: make(map[string]string),
		cl:     coverLoader,
		l:      l,
	}

	router.Handle("/", requireList(httperr.HandlerFunc(s.List))).Methods("GET")
	router.Handle("/{id}", requireGet(httperr.HandlerFunc(s.Get))).Methods("GET")
	router.Handle("/{id}/cover", httperr.HandlerFunc(s.GetCover)).Methods("GET")
	return s.fill(b)
}

func (s *songsAPI) fill(b storage.Backend) error {
	r := b.GetAll()
	var all []model.Song
	for r.Next() {
		song := r.Song()
		sourceS := filepath.Join(song.Dir, song.SourceFile)
		if song.Artist == "" && song.Title == "" {
			s.l.Warn("skipping song without title and artist",
				log.String("source", sourceS),
			)
			continue
		}
		source := enc.EncodeToString([]byte(sourceS))

		msong := model.Song{
			Title:  song.Title,
			Artist: song.Artist,
			Year:   song.Year,
		}
		s.songs[source] = msong
		s.covers[source] = filepath.Join(song.Dir, song.CoverPath)
		all = append(all, msong)
	}
	if r.Err() != nil {
		return r.Err()
	}
	data, err := json.Marshal(all)
	if err != nil {
		return err
	}
	s.allSongs = string(data)
	s.l.Debug("serialized songs",
		log.Int("songs", len(all)),
		log.Int("length", len(s.allSongs)),
	)
	return nil
}

func (s *songsAPI) List(w http.ResponseWriter, r *http.Request) error {
	s.l.Debug("writing songs",
		log.Int("length", len(s.allSongs)),
	)
	io.WriteString(w, s.allSongs)
	return nil
}

func (s *songsAPI) Get(w http.ResponseWriter, r *http.Request) error {
	jw, _ := httpjson.Wrap(w, r)

	source, ok := mux.Vars(r)["id"]
	if !ok {
		return httperr.WithCode(errors.New("id missing"), http.StatusBadRequest)
	}

	song, ok := s.songs[source]
	if !ok {
		return httperr.WithCode(errors.New("song not found"), http.StatusNotFound)
	}

	jw.Encode(song)
	return nil
}

func (s *songsAPI) GetCover(w http.ResponseWriter, r *http.Request) error {
	source, ok := mux.Vars(r)["id"]
	if !ok {
		return httperr.WithCode(errors.New("id missing"), http.StatusBadRequest)
	}

	coverP, ok := s.covers[source]
	if !ok {
		return httperr.WithCode(errors.New("song not found"), http.StatusNotFound)
	}

	data, etag, err := s.cl.Get(coverP)
	if err != nil {
		return err
	}

	etagS := enc.EncodeToString(etag)

	if cEtag := r.Header.Get("If-None-Match"); cEtag == etagS {
		w.WriteHeader(http.StatusNotModified)
		return nil
	}

	if len(etag) > 0 {
		w.Header().Set("Etag", etagS)
	}
	w.Header().Set("Cache-Control", "public, max-age=600")
	w.Write(data)

	return nil
}
