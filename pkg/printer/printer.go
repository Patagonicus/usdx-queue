package printer

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"io"
	"sync"
	"text/template"
	"time"

	"github.com/Patagonicus/usdx-queue/pkg/log"
	"github.com/Patagonicus/usdx-queue/pkg/model"
	qrcode "github.com/skip2/go-qrcode"
)

var tmpl = template.Must(template.New("foo").Funcs(Funcs).Parse(`{{reset}}{{.DateTime}}
{{big}}{{center}}+++ ULTRASTAR +++{{reset}}
Deine Ticketnummer und PIN:
{{big}}#{{.ID}}{{aligncolumn}}{{.PIN}}{{reset}}
Jetzt Namen eintragen und Song raussuchen:
{{center}}{{qr .Registration.URL}}{{bold}}{{center}}{{.Registration.Base}}{{reset}}
{{center}}+++ Reminder +++
Die Nummern werden angezeigt.
{{cut}}`))

var Funcs = map[string]interface{}{
	"altfont":     ret(altfont),
	"big":         ret(big),
	"double":      ret(double),
	"bold":        ret(bold),
	"reset":       ret(reset),
	"center":      ret(center),
	"aligncolumn": ret(alignColumn),
	"cut":         ret(cut),
	"image":       img,
	"qr":          qr,
}

func ret(s string) func() string {
	return func() string {
		return s
	}
}

const esc = "\x1b"

const (
	big         = bold + double
	altfont     = esc + "\x21\x10"
	bold        = esc + "\x47\x01"
	double      = esc + "\x57\x01" + esc + "\x68\x01"
	reset       = esc + "\x21\x10" + esc + "\x61\x00"
	center      = esc + "\x61\x01"
	alignColumn = esc + "\x61\x04"
	cut         = "\f"
)

var (
	verifyCompleted = []byte{0x1B, 0x00, 0x80, 0x00}
)

type Printer interface {
	Print(id model.ID, pin model.PIN, regBase, regURL string) error
}

type nilPrinter struct {
	l log.Logger
}

func (p nilPrinter) Print(id model.ID, pin model.PIN, regBase, regURL string) error {
	p.l.Info("printing ticket",
		log.Any("id", id),
		log.Any("pin", pin),
		log.String("regBase", regBase),
		log.String("regURL", regURL),
	)
	return nil
}

func NewNil(l log.Logger) Printer {
	return nilPrinter{l}
}

type printer struct {
	w io.Writer
	//msgC <-chan []byte
	m *sync.Mutex
	l log.Logger
}

func New(l log.Logger, rw io.ReadWriter) Printer {
	//msgC := make(chan []byte)
	//go reader(l, rw, msgC)
	return printer{
		w: rw,
		//msgC: msgC,
		m: new(sync.Mutex),
		l: l,
	}
}

func reader(l log.Logger, r io.Reader, msgC chan<- []byte) {
	defer close(msgC)
	r = filter{r, '\x11'}
	for {
		var sizeB [2]byte
		_, err := io.ReadFull(r, sizeB[:])
		if err != nil {
			l.Error("failed to read size",
				log.Error(err),
			)
			return
		}
		size := binary.BigEndian.Uint16(sizeB[:])
		buf := make([]byte, size-2)
		_, err = io.ReadFull(r, buf)
		if err != nil {
			l.Error("failed to read data",
				log.Error(err),
			)
			return
		}
		msgC <- buf
	}
}

type filter struct {
	r io.Reader
	b byte
}

func (f filter) Read(p []byte) (int, error) {
	n, err := f.r.Read(p)
	j := 0
	for i := 0; i < n; i++ {
		if p[i] != f.b {
			p[j] = p[i]
			j++
		}
	}
	return j, err
}

func (p printer) Print(id model.ID, pin model.PIN, regBase, regURL string) error {
	p.m.Lock()
	defer p.m.Unlock()

	p.l.Info("printing ticket",
		log.Any("id", id),
		log.Any("pin", pin),
		log.String("regBase", regBase),
		log.String("regURL", regURL),
	)

	err := tmpl.Execute(p.w, map[string]interface{}{
		"DateTime": time.Now().Format("02.01.2006 15:04"),
		"ID":       string(id),
		"PIN":      string(pin),
		"Registration": map[string]string{
			"Base": regBase,
			"URL":  regURL,
		},
	})
	if err != nil {
		return err
	}

	_, err = p.w.Write(verifyCompleted)
	if err != nil {
		return err
	}

	/*a
	_, ok := <-p.msgC
	if !ok {
		return errors.New("failed to read from printer")
	}
	*/
	return nil
}

func img(img image.Image) (string, error) {
	data, err := ImageToPrinter(img)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func qr(data string) (string, error) {
	qr, err := qrcode.New(data, qrcode.Low)
	if err != nil {
		return "", err
	}

	return img(qr.Image(-4))
}

func ImageToPrinter(img image.Image) ([]byte, error) {
	buf := &bytes.Buffer{}
	max := img.Bounds().Max
	if max.X > 59*8 {
		return nil, errors.New("image too wide")
	}
	rounds := max.Y / (5 * 8)
	for i := 0; i < rounds; i++ {
		encodeImage(img, buf, i*5*8, 5*8)
	}
	if rounds*5*8 < max.Y {
		encodeImage(img, buf, rounds*5*8, max.Y-rounds*5*8)
	}
	return buf.Bytes(), nil
}

func encodeImage(img image.Image, buf *bytes.Buffer, yoff, height int) {
	max := img.Bounds().Max
	w, h := max.X/8, height/8
	if max.X%8 != 0 {
		w++
	}
	if height%8 != 0 {
		h++
	}

	buf.Write([]byte{0x1B, 0x2A, 0x02, byte(w), byte(h)})
	for y := 0; y < h*8; y++ {
		for x := 0; x < max.X; x += 8 {
			var d byte
			for i := 0; i < 8; i++ {
				if isBlack(img, x+(7-i), yoff+y) {
					d |= 1 << uint(i)
				}
			}
			buf.WriteByte(d)
		}
	}
}

func isBlack(img image.Image, x, y int) bool {
	if x < 0 || y < 0 || x >= img.Bounds().Max.X || y >= img.Bounds().Max.Y {
		return false
	}

	r, g, b, _ := img.At(x, y).RGBA()
	return r == 0 && g == 0 && b == 0
}
