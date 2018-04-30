package httpjson

import (
	"encoding/json"
	"net/http"
)

func HandlerFunc(f func(w JSONResponseWriter, r *JSONRequest)) http.Handler {
	return handler{f}
}

type handler struct {
	f func(w JSONResponseWriter, r *JSONRequest)
}

func (h handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.f(Wrap(w, r))
}

func Wrap(w http.ResponseWriter, r *http.Request) (JSONResponseWriter, *JSONRequest) {
	return JSONResponseWriter{w, nil}, &JSONRequest{*r, nil}
}

type JSONResponseWriter struct {
	http.ResponseWriter
	encoder *json.Encoder
}

func (j JSONResponseWriter) Encode(v interface{}) error {
	if j.encoder == nil {
		j.Header().Set("Content-Type", "application/json")
		j.encoder = json.NewEncoder(j)
	}
	return j.encoder.Encode(v)
}

type JSONRequest struct {
	http.Request
	decoder *json.Decoder
}

func (j *JSONRequest) Decode(v interface{}) error {
	if j.decoder == nil {
		j.decoder = json.NewDecoder(j.Body)
	}
	return j.decoder.Decode(v)
}
