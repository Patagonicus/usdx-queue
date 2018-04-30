package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"time"

	"github.com/Patagonicus/group"
	"github.com/Patagonicus/usdx-queue/pkg/auth"
	"github.com/Patagonicus/usdx-queue/pkg/client"
	"github.com/Patagonicus/usdx-queue/pkg/log"
	"github.com/Patagonicus/usdx-queue/pkg/printer"
	"github.com/Patagonicus/usdx-queue/pkg/templates"
	"github.com/gorilla/mux"
	"github.com/jacobsa/go-serial/serial"
	"github.com/kelseyhightower/envconfig"
)

var errInterrupted = errors.New("interrupted")

type urlDecoder url.URL

func (u *urlDecoder) Decode(value string) error {
	decoded, err := url.Parse(value)
	*u = urlDecoder(*decoded)
	return err
}

type Config struct {
	Listen  string      `default:":8081"`
	Backend *urlDecoder `default:"http://localhost:8080"`
	Token   auth.Token  `required:"true"`
	Printer string      `default:"/dev/ttyUSB0"`
	WebBase string      `required:"true"`
}

func main() {
	l := log.NewDevelopment()
	defer l.Sync()

	var c Config
	err := envconfig.Process("usdx", &c)
	if err != nil {
		l.Fatal("failed to read config",
			log.Error(err),
		)
	}

	l.Info("config",
		log.Any("config", c),
	)

	client := client.New(l.Named("client"), (*url.URL)(c.Backend), c.Token)
	printer, printerActor, err := createPrinterActor(l.Named("printer"), c.Printer)
	if err != nil {
		l.Fatal("failed to open printer",
			log.Error(err),
		)
	}

	err = group.Run(
		createServerActor(l.Named("server"), c.Listen, client, printer, c.WebBase),
		createInterruptActor(l.Named("interrupt")),
		printerActor,
	)
	if err != nil && err != errInterrupted {
		l.Error("error running server",
			log.Error(err),
		)
	}
}

type server struct {
	indexTmpl  templates.Template
	createTmpl templates.Template
	ticketTmpl templates.Template
	client     client.Client
	printer    printer.Printer
	webBase    string
	l          log.Logger
}

func (s server) index(w http.ResponseWriter, r *http.Request) {
	s.indexTmpl.Execute(w, nil)
}

func (s server) create(w http.ResponseWriter, r *http.Request) {
	s.l.Debug("create")
	w.Header().Set("Refresh", "0;url=ticket")
	err := s.createTmpl.Execute(w, nil)
	if err != nil {
		s.l.Error("failed to execute create template",
			log.Error(err),
		)
	}
}

func (s server) ticket(w http.ResponseWriter, r *http.Request) {
	s.l.Debug("creating new ticket")
	w.Header().Set("Refresh", "5;url=index")

	id, pin, err := s.client.CreateTicket()
	if err != nil {
		s.l.Error("failed to create ticket",
			log.Error(err),
		)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.l.Info("created ticket",
		log.Any("id", id),
		log.Any("pin", pin),
	)

	err = s.printer.Print(id, pin, s.webBase, fmt.Sprintf("%s/index#edit/%s/%s", s.webBase, url.PathEscape(string(id)), url.PathEscape(string(pin))))
	printFailed := false
	if err != nil {
		s.l.Error("failed to print ticket",
			log.Error(err),
			log.Any("id", id),
			log.Any("pin", pin),
		)
		w.Header().Set("Refresh", "10;url=index")
		printFailed = true
	}

	s.ticketTmpl.Execute(w, map[string]interface{}{
		"ID":          string(id),
		"PIN":         string(pin),
		"printfailed": printFailed,
	})
}

func createServerActor(l log.Logger, listen string, client client.Client, printer printer.Printer, webBase string) group.Actor {
	s := server{
		indexTmpl:  templates.Must(templates.Create("registration/index.html")),
		createTmpl: templates.Must(templates.Create("registration/create.html")),
		ticketTmpl: templates.Must(templates.Create("registration/ticket.html")),
		client:     client,
		printer:    printer,
		webBase:    webBase,
		l:          l,
	}

	handler := mux.NewRouter()
	handler.HandleFunc("/", s.index)
	handler.HandleFunc("/index", s.index)
	handler.HandleFunc("/create", s.create)
	handler.HandleFunc("/ticket", s.ticket)

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

func createPrinterActor(l log.Logger, path string) (printer.Printer, group.Actor, error) {
	if path == "." {
		return printer.NewNil(l), group.WithChannel(func(c <-chan struct{}) error {
			<-c
			return nil
		}), nil
	}

	s, err := serial.Open(serial.OpenOptions{
		PortName:              path,
		BaudRate:              19200,
		DataBits:              8,
		StopBits:              1,
		MinimumReadSize:       1,
		InterCharacterTimeout: 1000,
	})
	if err != nil {
		return nil, group.Done(err), err
	}

	c := make(chan struct{})

	return printer.New(l, s), group.New(
		func() error {
			<-c
			return nil
		},
		func() {
			s.Close()
			close(c)
		},
	), nil
}
