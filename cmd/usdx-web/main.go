package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

func (u *urlDecoder) String() string {
	return (*url.URL)(u).String()
}

type Config struct {
	Listen  string      `default:":8083"`
	Backend *urlDecoder `default:"http://localhost:8080"`
	Pub     *urlDecoder
	Token   auth.Token `required:"true"`
}

func main() {
	//l := log.NewProduction()
	l := log.NewDevelopment()
	defer l.Sync()

	var c Config
	err := envconfig.Process("usdx", &c)
	if err != nil {
		l.Fatal("failed to load config",
			log.Error(err),
		)
	}

	l.Info("loaded config",
		log.String("listen", c.Listen),
		log.Stringer("backend", c.Backend),
		log.Stringer("pub", c.Pub),
		log.Any("token", c.Token),
	)

	if c.Pub.Host == "" {
		l.Info("using backend as public address",
			log.Stringer("backend", c.Backend),
		)
		c.Pub = c.Backend
	}

	client := client.NewWithPub(l.Named("client"), (*url.URL)(c.Backend), (*url.URL)(c.Pub), c.Token)

	group.Run(
		createServerActor(l.Named("server"), c.Listen, client),
		createInterruptActor(l.Named("interrupt")),
	)
}

type queue struct {
	Past     []model.Ticket
	Current  model.Ticket
	Upcoming []model.Ticket
}

type frontend struct {
	queueTmpl   templates.Template
	editTmpl    templates.Template
	songsTmpl   templates.Template
	playingTmpl templates.Template
	adminTmpl   templates.Template
	cssTmpl     templates.Template
	index       templates.Resource
	client      client.Client
	cachedSongs *cachedPage
	l           log.Logger
}

func (f frontend) Queue(w http.ResponseWriter, r *http.Request) error {
	queue, err := f.getQueue()
	if err != nil {
		return err
	}

	err = f.queueTmpl.Execute(w, map[string]interface{}{
		"Queue": queue,
	})
	if err != nil {
		f.l.Error("failed to render queue",
			log.Error(err),
		)
	}
	return nil
}

func (f frontend) Edit(w http.ResponseWriter, r *http.Request) error {
	err := f.editTmpl.Execute(w, map[string]interface{}{
		"ID":    r.FormValue("id"),
		"PIN":   r.FormValue("pin"),
		"Name1": r.FormValue("name1"),
		"Name2": r.FormValue("name2"),
		"Name3": r.FormValue("name3"),
		"Name4": r.FormValue("name4"),
		"Error": r.FormValue("error"),
		"Msg":   r.FormValue("msg"),
	})
	if err != nil {
		f.l.Error("failed to render edit",
			log.Error(err),
		)
	}
	return nil
}

func (f frontend) Save(w http.ResponseWriter, r *http.Request) {
	var (
		id    = r.FormValue("id")
		pin   = r.FormValue("pin")
		names = trimEmptyRight([]string{
			r.FormValue("name1"),
			r.FormValue("name2"),
			r.FormValue("name3"),
			r.FormValue("name4"),
		})
	)

	values := make(url.Values)
	for k, v := range r.Form {
		values[k] = v
	}
	values.Del("msg")
	values.Del("error")

	if id == "" {
		values.Set("error", "Ticketnummer fehlt")
		http.Redirect(w, r, "edit?"+values.Encode(), http.StatusSeeOther)
		return
	}

	if pin == "" {
		values.Set("error", "PIN fehlt")
		http.Redirect(w, r, "edit?"+values.Encode(), http.StatusSeeOther)
		return
	}

	err := f.client.SetNames(model.ID(id), model.PIN(pin), names)
	switch {
	case err == client.ErrPINInvalid:
		values.Set("error", "PIN ungültig")
		http.Redirect(w, r, "edit?"+values.Encode(), http.StatusSeeOther)
		return
	case err != nil:
		values.Set("error", err.Error())
		http.Redirect(w, r, "edit?"+values.Encode(), http.StatusSeeOther)
		return
	}

	values.Set("msg", "Namen gespeichert.")
	http.Redirect(w, r, "edit?"+values.Encode(), http.StatusSeeOther)
}

func (f frontend) Songs(w http.ResponseWriter, r *http.Request) error {
	data, gz, etag := f.cachedSongs.Get()
	if data == "" {
		return errors.New("error rendering page")
	}

	if etag != "" {
		if cEtag := r.Header.Get("If-None-Match"); cEtag == etag {
			w.WriteHeader(http.StatusNotModified)
			return nil
		}

		w.Header().Set("Etag", etag)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf8")

	if gz != "" && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		io.WriteString(w, gz)
		return nil
	}

	io.WriteString(w, data)
	return nil
}

func (f frontend) CSS(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Content-Type", "text/css")
	return f.cssTmpl.Execute(w, nil)
}

func (f frontend) renderSongs() []byte {
	f.l.Debug("fetching songs")
	songs, err := f.client.GetSongs()
	if err != nil {
		f.l.Error("failed to load songs",
			log.Error(err),
		)
		return nil
	}

	sort.Slice(songs, func(i, j int) bool {
		switch {
		case songs[i].Artist == songs[j].Artist:
			return songs[i].Title < songs[j].Title
		default:
			return songs[i].Artist < songs[j].Artist
		}
	})

	buf := &bytes.Buffer{}
	err = f.songsTmpl.Execute(buf, songs)
	if err != nil {
		f.l.Error("failed to render songs",
			log.Error(err),
		)
		return nil
	}
	return buf.Bytes()
}

type song struct {
	Title          string
	Artist         string
	Length         time.Duration
	CoverURL       string
	Source         string
	CompletionPerc int
}

type state struct {
	Empty     bool
	CurrentID string
	Waiting   int
	HasSong   bool
	Paused    bool
	Position  time.Duration
	Song      song
}

type ticketJSON struct {
	ID    string   `json:"id"`
	Names []string `json:"names"`
}

type queueJSON struct {
	Previous []ticketJSON `json:"previous"`
	Upcoming []ticketJSON `json:"upcoming"`
}

func (f frontend) Playing(w http.ResponseWriter, r *http.Request) {
	s, err := f.getState()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	f.l.Debug("rendering",
		log.Any("state", s),
	)
	err = f.playingTmpl.Execute(w, s)
	if err != nil {
		f.l.Warn("failed to execute template",
			log.Error(err),
		)
	}
}

func (f frontend) Admin(w http.ResponseWriter, r *http.Request) error {
	user, pass, ok := r.BasicAuth()
	f.l.Debug("admin page requested",
		log.String("user", user),
		log.String("pass", pass),
	)
	if !ok || "admin" != user || "einsvorne" != pass {
		w.Header().Set("WWW-Authenticate", `Basic ream="usdx"`)
		return httperr.WithCode(errors.New("Unauthorized"), http.StatusUnauthorized)
	}

	if r.FormValue("action") != "" {
		var err error
		var msg string
		switch r.FormValue("action") {
		case "pause":
			err = f.client.TogglePause()
			if err != nil {
				msg = "Pause toggled"
			}
		case "advance":
			err = f.client.Advance()
		case "goback":
			err = f.client.GoBack()
		}
		values := make(url.Values)
		if err != nil {
			values.Set("err", err.Error())
		}
		values.Set("msg", msg)
		http.Redirect(w, r, "admin?"+values.Encode(), http.StatusFound)
		return nil
	}

	queue, err := f.client.GetQueue()
	if err != nil {
		return err
	}

	var curID string
	if queue.Position < len(queue.Queue) {
		curID = string(queue.Queue[queue.Position])
	}
	upcoming := len(queue.Queue) - queue.Position - 1
	if upcoming < 0 {
		upcoming = 0
	}

	tickets, err := f.client.GetTickets()
	if err != nil {
		return err
	}
	sort.Slice(tickets, func(i, j int) bool {
		a, _ := strconv.Atoi(string(tickets[i].ID))
		b, _ := strconv.Atoi(string(tickets[j].ID))
		return a < b
	})

	f.adminTmpl.Execute(w, map[string]interface{}{
		"Paused":   queue.Paused,
		"Current":  curID,
		"Upcoming": upcoming,
		"Tickets":  tickets,
		"Error":    r.FormValue("err"),
		"Msg":      r.FormValue("msg"),
	})
	return nil
}

func (f frontend) APIQueue(w http.ResponseWriter, r *http.Request) error {
	queue, err := f.getQueue()
	if err != nil {
		f.l.Warn("failed to get queue",
			log.Error(err),
		)
		return err
	}

	j := queueJSON{}
	j.Previous = make([]ticketJSON, len(queue.Past))
	for i, t := range queue.Past {
		j.Previous[i] = ticketJSON{
			ID:    string(t.ID),
			Names: t.Names,
		}
	}

	if queue.Current.ID != model.ID("") {
		j.Upcoming = []ticketJSON{
			{
				ID:    string(queue.Current.ID),
				Names: queue.Current.Names,
			},
		}
	}
	for _, t := range queue.Upcoming {
		j.Upcoming = append(j.Upcoming, ticketJSON{
			ID:    string(t.ID),
			Names: t.Names,
		})
	}

	json.NewEncoder(w).Encode(j)
	return nil
}

func (f frontend) APISongs(w http.ResponseWriter, r *http.Request) error {
	songs, err := f.client.GetSongs()
	if err != nil {
		f.l.Warn("failed to get songs",
			log.Error(err),
		)
		return err
	}

	sort.Slice(songs, func(i, j int) bool {
		switch {
		case songs[i].Artist < songs[j].Artist:
			return true
		case songs[i].Artist > songs[j].Artist:
			return false
		default:
			return songs[i].Title < songs[j].Title
		}
	})

	json.NewEncoder(w).Encode(songs)
	return nil
}

func (f frontend) APISave(w http.ResponseWriter, r *http.Request) error {
	var request struct {
		ID    string   `json:"id"`
		PIN   string   `json:"pin"`
		Names []string `json:"names"`
	}
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		return err
	}

	err = f.client.SetNames(model.ID(request.ID), model.PIN(request.PIN), request.Names)
	switch {
	case err == client.ErrPINInvalid:
		return httperr.WithCode(err, http.StatusUnauthorized)
	case err != nil:
		return err
	}

	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (f frontend) getState() (state, error) {
	f.l.Debug("getting queue")
	queue, err := f.client.GetQueue()
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

	playbackState, err := f.client.GetState()
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
		song, err := f.client.GetSong(playbackState.Source)
		if err == nil {
			cur.Song.Artist = song.Artist
			cur.Song.Title = song.Title
		}
		cur.Song.CoverURL = f.client.GetCoverURL(playbackState.Source)
		cur.Song.CompletionPerc = int(playbackState.RelPos() * 100)
	} else {
		f.l.Warn("failed to get playback state",
			log.Error(err),
		)
	}

	return cur, nil
}

func trimEmptyRight(s []string) []string {
	l := len(s) - 1
	for l >= 0 && s[l] == "" {
		l--
	}
	return s[:l+1]
}

func (f frontend) getQueue() (queue, error) {
	mQueue, err := f.client.GetQueue()
	if err != nil {
		return queue{}, err
	}

	tickets, err := f.client.GetTickets()
	if err != nil {
		f.l.Warn("failed to load tickets",
			log.Error(err),
		)
	}

	byID := make(map[model.ID]model.Ticket, len(tickets))
	for _, t := range tickets {
		byID[t.ID] = t
	}

	var q queue
	for i := 0; i < mQueue.Position; i++ {
		id := mQueue.Queue[i]
		t, ok := byID[id]
		if !ok {
			t.ID = id
		}
		q.Past = append(q.Past, t)
	}
	if mQueue.Position >= 0 && mQueue.Position < len(mQueue.Queue) {
		id := mQueue.Queue[mQueue.Position]
		t, ok := byID[id]
		if !ok {
			t.ID = id
		}
		q.Current = t
	}
	for i := mQueue.Position + 1; i < len(mQueue.Queue); i++ {
		id := mQueue.Queue[i]
		t, ok := byID[id]
		if !ok {
			t.ID = id
		}
		q.Upcoming = append(q.Upcoming, t)
	}
	return q, nil
}

func (f frontend) getUpcoming() ([]model.Ticket, error) {
	return nil, nil
}

func createServerActor(l log.Logger, listen string, client client.Client) group.Actor {
	f := frontend{
		queueTmpl:   templates.Must(templates.Create("web/queue.html")),
		editTmpl:    templates.Must(templates.Create("web/edit.html")),
		songsTmpl:   templates.Must(templates.Create("web/songs.html")),
		playingTmpl: templates.Must(templates.CreateWithFuncs("web/playing.html", map[string]interface{}{"formatDuration": formatDuration})),
		adminTmpl:   templates.Must(templates.Create("web/admin.html")),
		cssTmpl:     templates.Must(templates.Create("web/web.css")),
		index:       templates.MustResource(templates.NewResource("web/index.html")),
		client:      client,
		l:           l,
	}
	f.cachedSongs = newCachedPage(l.Named("songs"), f.renderSongs)

	handler := mux.NewRouter()
	handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/index#/", http.StatusFound)
	})
	handler.HandleFunc("/songs", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/index#/songs", http.StatusFound)
	})
	handler.HandleFunc("/queue", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/index#/queue", http.StatusFound)
	})
	//handler.Handle("/", httperr.HandlerFunc(f.Queue))
	handler.Handle("/api/queue", httperr.HandlerFunc(f.APIQueue))
	handler.Handle("/api/songs", httperr.HandlerFunc(f.APISongs))
	handler.Handle("/api/save", httperr.HandlerFunc(f.APISave))
	//handler.Handle("/queue", httperr.HandlerFunc(f.Queue))
	//handler.Handle("/edit", httperr.HandlerFunc(f.Edit))
	//handler.HandleFunc("/save", f.Save)
	handler.Handle("/songs", httperr.HandlerFunc(f.Songs))
	handler.HandleFunc("/playing", f.Playing)
	handler.Handle("/admin", httperr.HandlerFunc(f.Admin))
	handler.Handle("/web.css", httperr.HandlerFunc(f.CSS))
	handler.Handle("/index", templates.ServeResource(f.index))
	handler.Handle("/moon.js", templates.ServeResource(templates.MustResource(templates.NewResource("common/moon.js"))))
	handler.Handle("/moon-dev.js", templates.ServeResource(templates.MustResource(templates.NewResource("common/moon-dev.js"))))
	handler.Handle("/moon-router.js", templates.ServeResource(templates.MustResource(templates.NewResource("common/moon-router.js"))))
	handler.Handle("/vue.js", templates.ServeResource(templates.MustResource(templates.NewResource("common/vue.js"))))
	handler.Handle("/vue-router.js", templates.ServeResource(templates.MustResource(templates.NewResource("common/vue-router.js"))))
	for _, s := range []struct {
		p string
		m string
	}{
		{"material-icons.css", "text/css"},
		{"MaterialIcons-Regular.eot", "application/vnd.ms-fontobject"},
		{"MaterialIcons-Regular.ijmap", ""},
		{"MaterialIcons-Regular.svg", "image/svg+xml"},
		{"MaterialIcons-Regular.ttf", "application/font-ttf"},
		{"MaterialIcons-Regular.woff", "application/font-woff"},
		{"MaterialIcons-Regular.woff2", "font/woff2"},
	} {
		hp := path.Join("/material", s.p)
		rp := path.Join("material", s.p)
		handler.Handle(hp, templates.ServeResourceMime(templates.MustResource(templates.NewResource(rp)), s.m))
	}

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

type entry struct {
	rendered string
	etag     string
	gz       string
	once     *sync.Once
	at       time.Time
}

type cachedPage struct {
	f     func() []byte
	entry *atomic.Value
	l     log.Logger
}

func newCachedPage(l log.Logger, f func() []byte) *cachedPage {
	val := new(atomic.Value)
	val.Store(&entry{
		once: new(sync.Once),
	})
	return &cachedPage{
		f:     f,
		entry: val,
		l:     l,
	}
}

func (c *cachedPage) Get() (string, string, string) {
	now := time.Now()
	for {
		e := c.entry.Load().(*entry)
		if e.at.Add(10 * time.Minute).Before(now) {
			c.l.Debug("entry outdated, updating")
			e.once.Do(func() {
				c.update()
			})
			continue
		}
		c.l.Debug("returning entry")
		return e.rendered, e.gz, e.etag
	}
}

func (c *cachedPage) update() {
	var e entry
	c.l.Debug("calling f")
	data := c.f()
	c.l.Debug("f returned")
	e.rendered = string(data)
	sha := sha512.Sum512(data)
	e.etag = base64.StdEncoding.EncodeToString(sha[:])
	e.once = new(sync.Once)
	e.at = time.Now()
	e.gz = c.compress(e.rendered)
	c.l.Debug("storing entry")
	c.entry.Store(&e)
}

func (c *cachedPage) compress(d string) string {
	buf := &bytes.Buffer{}
	w := gzip.NewWriter(buf)
	_, err := io.WriteString(w, d)
	if err != nil {
		c.l.Warn("failed to compress page",
			log.Error(err),
		)
		return ""
	}
	err = w.Close()
	if err != nil {
		c.l.Warn("failed to compress page",
			log.Error(err),
		)
		return ""
	}
	return buf.String()
}
