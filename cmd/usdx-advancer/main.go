package main

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Patagonicus/group"
	"github.com/Patagonicus/usdx-queue/pkg/auth"
	"github.com/Patagonicus/usdx-queue/pkg/client"
	"github.com/Patagonicus/usdx-queue/pkg/log"
	"github.com/Patagonicus/usdx-queue/pkg/model"
	"github.com/fsnotify/fsnotify"
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
	Path    string      `default:"/run/usdx"`
	Backend *urlDecoder `default:"http://localhost:8080"`
	Token   auth.Token  `required:"true"`
	Base    string      `required:"true"`
}

func main() {
	status := 0
	defer os.Exit(status)

	l := log.NewDevelopment()
	defer l.Sync()

	var c Config
	err := envconfig.Process("usdx", &c)
	if err != nil {
		l.Error("failed to load config",
			log.Error(err),
		)
		status = 1
		runtime.Goexit()
	}

	client := client.New(l.Named("client"), (*url.URL)(c.Backend), c.Token)
	pathC := make(chan string)
	watcher, err := createWatcher(l.Named("watcher"), c.Path, pathC)
	if err != nil {
		l.Error("failed to watch directory",
			log.String("path", c.Path),
			log.Error(err),
		)
		status = 1
		runtime.Goexit()
	}
	err = group.Run(
		watcher,
		createNotifier(l.Named("notifier"), client, pathC, c.Base),
		createInterruptActor(l.Named("interrupt")),
	)
	if err != nil && err != errInterrupted {
		l.Error("error",
			log.Error(err),
		)
		status = 1
		runtime.Goexit()
	}
}

func createNotifier(l log.Logger, client client.Client, c <-chan string, baseDir string) group.Actor {
	scoreProcessed := make(map[string]bool)
	return group.WithChannel(func(done <-chan struct{}) error {
		var lastStart time.Time
		var running bool
		for {
			select {
			case path := <-c:
				path = filepath.Clean(path)
				l := l.With(log.String("path", path))
				l.Debug("handling event")
				base := filepath.Base(path)
				switch {
				case strings.HasSuffix(base, ".new"):
					l.Debug(".new file, skipping")
				case base == "state":
					state, err := loadState(path, baseDir)
					if err != nil {
						l.Warn("failed to load state",
							log.Error(err),
						)
						continue
					}
					l.Debug("loaded state",
						log.Any("state", state),
					)
					switch {
					case state.Playback == model.Stopped:
						running = false
					case !running:
						running = true
						lastStart = time.Now()
						l.Debug("new song started",
							log.Stringer("last", lastStart),
						)
					}
					err = client.SetState(state)
					if err != nil {
						l.Warn("failed to send state",
							log.Any("state", state),
							log.Error(err),
						)
					} else {
						l.Debug("set state")
					}
				case strings.HasPrefix(base, "score-"):
					if scoreProcessed[base] {
						l.Debug("score file already processed, skipping")
						break
					}
					scoreProcessed[base] = true
					now := time.Now()
					if lastStart.Add(30 * time.Second).After(now) {
						l.Warn("song lasted less than 30s, will not advance",
							log.Stringer("last", lastStart),
							log.Stringer("now", now),
						)
					}
					err := client.Advance()
					if err != nil {
						l.Warn("failed to advance queue",
							log.Error(err),
						)
					}
				default:
					l.Debug("unknown file, ignoring")
				}
			case <-done:
				return nil
			}
		}
	})
}

func loadState(path, baseDir string) (model.State, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return model.State{}, err
	}
	return parseState(strings.Split(string(data), "\n"), baseDir)
}

func parseState(lines []string, baseDir string) (model.State, error) {
	var s model.State

	lines = removeEmptyLastLine(lines)

	if len(lines) < 2 {
		return s, fmt.Errorf("expected at least 2 lines")
	}

	if lines[1] == "-" {
		s.Playback = model.Stopped
		return s, nil
	}

	if len(lines) < 5 {
		return s, fmt.Errorf("expected at least 5 lines")
	}

	source, err := filepath.Rel(baseDir, lines[1])
	if err != nil {
		fmt.Printf("failed to get relative path: %s\n", err)
		source = lines[1]
	}
	s.Source = source

	switch lines[0] {
	case "TRUE":
		s.Playback = model.Paused
	case "FALSE":
		s.Playback = model.Playing
	default:
		return s, fmt.Errorf("unknown value for pause: %s", lines[0])
	}

	s.Position, err = parseFloatDuration(strings.Trim(lines[2], " "))
	s.Length, err = parseFloatDuration(strings.Trim(lines[3], " "))

	s.Scores, err = parseScores(lines[4:])
	return s, err
}

func parseFloatDuration(s string) (time.Duration, error) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return time.Duration(0), err
	}
	return time.Duration(f * float64(time.Second)), nil
}

func parseScores(lines []string) ([]model.Score, error) {
	scores := make([]model.Score, len(lines))
	for i, line := range lines {
		_, err := fmt.Sscanf(line, "%d %d %d", &scores[i].Base, &scores[i].Line, &scores[i].Golden)
		if err != nil && err != io.EOF {
			return nil, err
		}
	}
	return scores, nil
}

func removeEmptyLastLine(lines []string) []string {
	switch {
	case len(lines) == 0:
		return lines
	case lines[len(lines)-1] == "":
		return lines[:len(lines)-1]
	default:
		return lines
	}
}

func createWatcher(l log.Logger, path string, c chan<- string) (group.Actor, error) {
	l = l.With(log.String("path", path))
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	err = w.Add(path)
	if err != nil {
		w.Close()
		return group.Done(err), err
	}
	return group.WithChannel(func(done <-chan struct{}) error {
		defer w.Close()
		defer close(c)
		l.Info("now watching directory")
		for {
			select {
			case event := <-w.Events:
				l.Debug("got event",
					log.Any("event", event),
				)
				if event.Op == fsnotify.Remove {
					l.Debug("is remove, ignoring",
						log.Any("event", event),
					)
					break
				}
				c <- event.Name
			case err = <-w.Errors:
				l.Warn("error watching",
					log.Error(err),
				)
			case <-done:
				return nil
			}
		}
	}), nil
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
