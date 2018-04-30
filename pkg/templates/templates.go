package templates

import (
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"html/template"
	"io"
	"net/http"
)

type Template interface {
	Execute(w io.Writer, data interface{}) error
}

func Must(t Template, err error) Template {
	if err != nil {
		panic(err)
	}
	return t
}

func Create(path string) (Template, error) {
	return create(path)
}

func CreateWithFuncs(path string, funcs map[string]interface{}) (Template, error) {
	return createWithFuncs(path, funcs)
}

func loadTemplate(path string, funcs map[string]interface{}) (Template, error) {
	data, err := AssetString(path)
	if err != nil {
		return nil, err
	}
	return template.New(path).Funcs(funcs).Parse(data)
}

type Resource interface {
	Get() (string, string, error)
}

func MustResource(r Resource, err error) Resource {
	if err != nil {
		panic(err)
	}
	return r
}

func NewResource(path string) (Resource, error) {
	return newResource(path)
}

func loadResource(path string) (string, string, error) {
	data, err := AssetString(path)
	if err != nil {
		return "", "", err
	}
	sha := sha512.New()
	_, err = io.WriteString(sha, data)
	if err != nil {
		return "", "", err
	}
	return data, base64.StdEncoding.EncodeToString(sha.Sum(nil)), nil
}

func ServeResource(res Resource) http.Handler {
	return ServeResourceMime(res, "")
}

func ServeResourceMime(res Resource, mime string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, etag, err := res.Get()
		if err != nil {
			http.Error(w, fmt.Sprintf("could not load resource: %s", err.Error()), http.StatusInternalServerError)
			return
		}

		if cEtag := r.Header.Get("ETag"); etag != "" && etag == cEtag {
			w.WriteHeader(http.StatusNotModified)
			return
		}

		if mime != "" {
			w.Header().Set("Content-Type", mime)
		}
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, data)
	})
}
