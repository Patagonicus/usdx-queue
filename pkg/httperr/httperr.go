package httperr

import (
	"encoding/json"
	"net/http"
)

type causer interface {
	Cause() error
}

type coder interface {
	Code() int
}

func WithCode(err error, code int) error {
	return withCode{err, code}
}

type withCode struct {
	err  error
	code int
}

func (w withCode) Error() string {
	return w.err.Error()
}

func (w withCode) Code() int {
	return w.code
}

func (w withCode) Cause() error {
	c, ok := w.err.(causer)
	if !ok {
		return nil
	}
	return c.Cause()
}

type handlerFunc struct {
	f func(w http.ResponseWriter, r *http.Request) error
}

func HandlerFunc(f func(w http.ResponseWriter, r *http.Request) error) http.Handler {
	return handlerFunc{f}
}

func (f handlerFunc) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	err := f.f(w, r)
	if err == nil {
		return
	}

	response := struct {
		Status struct {
			Code    int    `json:"code"`
			Message string `json:"message,omitempty"`
		} `json:"status"`
		Error  string   `json:"error"`
		Causes []string `json:"causes,omitempty"`
	}{}

	response.Error = err.Error()

	response.Status.Code = http.StatusInternalServerError
	if c, ok := err.(coder); ok {
		response.Status.Code = c.Code()
	}
	response.Status.Message = http.StatusText(response.Status.Code)

	response.Causes = causes(err)

	w.WriteHeader(response.Status.Code)
	json.NewEncoder(w).Encode(response)
}

func causes(err error) []string {
	var result []string
	for {
		c, ok := err.(causer)
		if !ok {
			return result
		}
		err := c.Cause()
		if err == nil {
			return result
		}
		result = append(result, err.Error())
	}
}
