package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"time"

	"github.com/Patagonicus/group"
	"github.com/Patagonicus/usdx-queue/pkg/auth"
	"github.com/Patagonicus/usdx-queue/pkg/client"
	"github.com/Patagonicus/usdx-queue/pkg/httperr"
	"github.com/Patagonicus/usdx-queue/pkg/log"
	"github.com/Patagonicus/usdx-queue/pkg/model"
	"github.com/Patagonicus/usdx-queue/pkg/templates"
	"github.com/gorilla/mux"
	"github.com/kelseyhightower/envconfig"
)

var errInterrupted = errors.New("interrupted")

type urlDecoder url.URL

func (u *urlDecoder) Decode(value string) error {
	decoded, err := url.Parse(value)
	*u = urlDecoder(*decoded)
	return err
}

var colors = []string{
	"Grün",
	"Blau",
	"Rot",
	"Gelb",
}

var hexColors = []string{
	"00ac00",
	"0064be",
	"d00000",
	"e7e700",
}

type Config struct {
	Listen  string      `default:":8082"`
	Backend *urlDecoder `default:"http://localhost:8080"`
	Pub     *urlDecoder
	Token   auth.Token `required:"true"`
}

func main() {
	l := log.NewDevelopment()
	defer l.Sync()

	var c Config
	err := envconfig.Process("usdx", &c)
	if err != nil {
		l.Fatal("failed to load config",
			log.Error(err),
		)
	}

	if c.Pub.Host == "" {
		l.Info("using backend for pub",
			log.Stringer("backend", (*url.URL)(c.Backend)),
		)
		c.Pub = c.Backend
	}

	client := client.NewWithPub(l.Named("client"), (*url.URL)(c.Backend), (*url.URL)(c.Pub), c.Token)

	err = group.Run(
		createServerActor(l.Named("server"), c.Listen, client),
		createInterruptActor(l.Named("interrupt")),
	)
	if err != nil && err != errInterrupted {
		l.Error("failed to run group",
			log.Error(err),
		)
	}
}

type song struct {
	Title          string
	Artist         string
	Length         time.Duration
	CoverURL       string
	Source         string
	CompletionPerc int
}

type score struct {
	Name          string
	Points        int
	RelPercentage int
	Color         string
}

type state struct {
	Empty     bool
	CurrentID string
	Waiting   int
	HasSong   bool
	Paused    bool
	Position  time.Duration
	Song      song
	Scores    []score
}

type ticketJSON struct {
	ID     string   `json:"id"`
	Names  []string `json:"names"`
	Colors []string `json:"colors"`
	Scores []int    `json:"scores"`
}

type songJSON struct {
	Title    string `json:"title"`
	Artist   string `json:"artist"`
	CoverURL string `json:"cover"`
	ElapsedS int    `json:"elapsed"`
	TotalS   int    `json:"total"`
}

type stateJSON struct {
	Ticket  ticketJSON `json:"ticket"`
	HasSong bool       `json:"hasSong"`
	Song    songJSON   `json:"song"`
	Waiting int        `json:"waiting"`
}

func fromState(s state) stateJSON {
	names := make([]string, len(s.Scores))
	colors := make([]string, len(s.Scores))
	scores := make([]int, len(s.Scores))
	for i, score := range s.Scores {
		names[i] = score.Name
		colors[i] = score.Color
		scores[i] = score.Points
	}

	return stateJSON{
		Ticket: ticketJSON{
			ID:     s.CurrentID,
			Names:  names,
			Colors: colors,
			Scores: scores,
		},
		HasSong: s.HasSong,
		Song: songJSON{
			Title:    s.Song.Title,
			Artist:   s.Song.Artist,
			CoverURL: s.Song.CoverURL,
			ElapsedS: int(s.Position / time.Second),
			TotalS:   int(s.Song.Length / time.Second),
		},
	}
}

type frontend struct {
	tmpl templates.Template
	bg   templates.Resource
	c    client.Client
	l    log.Logger
}

func (f frontend) Index(w http.ResponseWriter, r *http.Request) {
	//w.Header().Set("Refresh", "1")

	s, err := f.getState()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	f.l.Debug("rendering",
		log.Any("state", s),
	)
	err = f.tmpl.Execute(w, s)
	if err != nil {
		f.l.Warn("failed to execute template",
			log.Error(err),
		)
	}
}

func (f frontend) State(w http.ResponseWriter, r *http.Request) {
	s, err := f.getState()
	if err != nil {
		f.l.Warn("failed to get state",
			log.Error(err),
		)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	j := fromState(s)

	f.l.Debug("sending",
		log.Any("state", s),
		log.Any("stateJSON", j),
	)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	err = json.NewEncoder(w).Encode(j)
	if err != nil {
		f.l.Warn("error writing json state",
			log.Error(err),
		)
	}
}

func (f frontend) Background(w http.ResponseWriter, r *http.Request) error {
	data, etag, err := f.bg.Get()
	if err != nil {
		return err
	}
	if cEtag := r.Header.Get("If-None-Match"); cEtag == etag {
		w.WriteHeader(http.StatusNotModified)
		return nil
	}
	w.Header().Set("Etag", etag)
	io.WriteString(w, data)
	return nil
}

func (f frontend) getState() (state, error) {
	f.l.Debug("getting queue")
	queue, err := f.c.GetQueue()
	if err != nil {
		f.l.Error("failed to update state",
			log.Error(err),
		)
		return state{}, err
	}

	f.l.Debug("queue",
		log.Any("queue", queue),
	)

	var cur state
	if queue.Position >= len(queue.Queue) {
		f.l.Debug("pos >= len",
			log.Int("pos", queue.Position),
			log.Int("len", len(queue.Queue)),
		)
		cur.Empty = true
		cur.CurrentID = ""
		cur.Waiting = 0
	} else {
		f.l.Debug("pos < len",
			log.Int("pos", queue.Position),
			log.Int("len", len(queue.Queue)),
			log.String("current", string(queue.Queue[queue.Position])),
			log.Int("upcoming", len(queue.Queue)-queue.Position-1),
		)
		cur.Empty = false
		cur.CurrentID = string(queue.Queue[queue.Position])
		cur.Waiting = len(queue.Queue) - queue.Position - 1
	}

	f.l.Debug("waiting",
		log.Int("waiting", cur.Waiting),
		log.Int("queue", len(queue.Queue)),
		log.Int("pos", queue.Position),
	)

	var names []string
	if !cur.Empty {
		ticket, err := f.c.GetTicket(queue.Queue[queue.Position])
		if err != nil {
			f.l.Warn("failed to get current ticket",
				log.Error(err),
			)
		} else {
			names = ticket.Names
		}
	}

	playbackState, err := f.c.GetState()
	if err == nil {
		f.l.Debug("state",
			log.Any("state", playbackState),
		)
		switch playbackState.Playback {
		case model.Stopped:
			cur.HasSong = false
		case model.Playing:
			cur.HasSong = true
			cur.Paused = false
		case model.Paused:
			cur.HasSong = true
			cur.Paused = true
		}
		cur.Position = playbackState.Position
		cur.Song.Length = playbackState.Length
		cur.Song.Source = playbackState.Source
		if cur.HasSong {
			song, err := f.c.GetSong(playbackState.Source)
			if err == nil {
				cur.Song.Artist = song.Artist
				cur.Song.Title = song.Title
			}
			cur.Song.CoverURL = f.c.GetCoverURL(playbackState.Source)
			cur.Song.CompletionPerc = int(playbackState.RelPos() * 100)
		}
		max := 0
		for _, s := range playbackState.Scores {
			if s.Total() > max {
				max = s.Total()
			}
		}
		cur.Scores = make([]score, len(playbackState.Scores))
		for i, s := range playbackState.Scores {
			cur.Scores[i].Points = s.Total()
			cur.Scores[i].RelPercentage = int(float64(s.Total()) / float64(max) * 100)
			if cur.Scores[i].RelPercentage < 0 {
				cur.Scores[i].RelPercentage = 0
			}
			if cur.Scores[i].RelPercentage > 100 {
				cur.Scores[i].RelPercentage = 100
			}
			switch {
			case i < len(names) && names[i] != "":
				cur.Scores[i].Name = names[i]
			case i < len(colors):
				cur.Scores[i].Name = colors[i]
			default:
				cur.Scores[i].Name = fmt.Sprintf("Spieler %d", i)
			}
			if i < len(hexColors) {
				cur.Scores[i].Color = hexColors[i]
			}
		}
	} else {
		f.l.Warn("failed to get playback state",
			log.Error(err),
		)
	}

	if !cur.HasSong {
		cur.Scores = make([]score, len(names))
		for i, n := range names {
			cur.Scores[i].Name = n
		}
	}

	return cur, nil
}

func createServerActor(l log.Logger, listen string, client client.Client) group.Actor {
	f := frontend{
		tmpl: templates.Must(templates.CreateWithFuncs("beamer/index.html", map[string]interface{}{"formatDuration": formatDuration})),
		bg:   templates.MustResource(templates.NewResource("beamer/bg.png")),
		c:    client,
		l:    l,
	}

	small := templates.MustResource(templates.NewResource("beamer/small.html"))
	large := templates.MustResource(templates.NewResource("beamer/large.html"))
	css := templates.MustResource(templates.NewResource("beamer/beamer.css"))
	js := templates.MustResource(templates.NewResource("beamer/beamer.js"))
	handler := mux.NewRouter()
	handler.StrictSlash(true)
	//handler.HandleFunc("/", f.Index)
	handler.Handle("/", templates.ServeResource(large))
	handler.HandleFunc("/state", f.State)
	handler.Handle("/bg.png", httperr.HandlerFunc(f.Background))
	handler.Handle("/small", templates.ServeResource(small))
	handler.Handle("/large", templates.ServeResource(large))
	handler.Handle("/beamer.css", templates.ServeResourceMime(css, "text/css"))
	handler.Handle("/beamer.js", templates.ServeResourceMime(js, "application/javascript"))

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

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	m := d / time.Minute
	s := (d - m*time.Minute) / time.Second
	return fmt.Sprintf("%02d:%02d", m, s)
}
