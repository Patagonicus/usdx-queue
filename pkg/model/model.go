package model

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

type PIN string

func (p PIN) String() string {
	return string(p)
}

type ID string

func (id ID) String() string {
	return string(id)
}

type Version int64

var DontCare = Version(-1)

func (v Version) Conflict(other Version) bool {
	return v != DontCare && other != DontCare && v != other
}

func (v Version) String() string {
	if v == DontCare {
		return "Version{Don'tCare}"
	}
	return fmt.Sprintf("Version{%d}", v)
}

type Ticket struct {
	ID      ID       `json:"id"`
	Names   []string `json:"names,omitempty"`
	Version Version  `json:"version"`
}

func (t Ticket) String() string {
	return fmt.Sprintf("Ticket{%s %s %s}", t.ID, t.Names, t.Version)
}

type Queue struct {
	Queue    []ID
	Position int
	Paused   bool
	Version  Version
}

type PlaybackState int

const (
	Stopped PlaybackState = iota
	Playing
	Paused
)

func (p PlaybackState) String() string {
	switch p {
	case Stopped:
		return "stopped"
	case Playing:
		return "playing"
	case Paused:
		return "paused"
	default:
		return strconv.Itoa(int(p))
	}
}

func FromString(s string) PlaybackState {
	switch s {
	case "stopped":
		return Stopped
	case "playing":
		return Playing
	case "paused":
		return Paused
	default:
		p, err := strconv.Atoi(s)
		if err != nil {
			return Stopped
		}
		return PlaybackState(p)
	}
}

type State struct {
	Playback PlaybackState
	Source   string
	Position time.Duration
	Length   time.Duration
	Scores   []Score
}

func (s State) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Playback string
		Source   string
		Position time.Duration
		Length   time.Duration
		Scores   []Score
	}{
		Playback: s.Playback.String(),
		Source:   s.Source,
		Position: s.Position,
		Length:   s.Length,
		Scores:   s.Scores,
	})
}

func (s *State) UnmarshalJSON(data []byte) error {
	var t struct {
		Playback string
		Source   string
		Position time.Duration
		Length   time.Duration
		Scores   []Score
	}
	err := json.Unmarshal(data, &t)
	if err != nil {
		return err
	}
	s.Playback = FromString(t.Playback)
	s.Source = t.Source
	s.Position = t.Position
	s.Length = t.Length
	s.Scores = t.Scores
	return nil
}

func (s State) RelPos() float64 {
	switch {
	case s.Playback == Stopped:
		return 0
	case s.Position < 0:
		return 0
	case s.Position > s.Length:
		return 1
	default:
		return float64(s.Position) / float64(s.Length)
	}
}

type Score struct {
	Base   int
	Line   int
	Golden int
}

func (s Score) Total() int {
	return s.Base + s.Line + s.Golden
}

type Song struct {
	Title  string `json:"title"`
	Artist string `json:"artist"`
	Year   int    `json:"year"`
}
