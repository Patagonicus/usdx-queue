// +build development

package templates

import (
	"io"
)

//go:generate go-bindata -pkg $GOPACKAGE -ignore .*\.go -nometadata -o assets.go -debug ./...

type tmpl struct {
	path  string
	funcs map[string]interface{}
}

func (t tmpl) Execute(w io.Writer, data interface{}) error {
	tmpl, err := loadTemplate(t.path, t.funcs)
	if err != nil {
		return err
	}
	return tmpl.Execute(w, data)
}

func create(path string) (Template, error) {
	return createWithFuncs(path, make(map[string]interface{}))
}

func createWithFuncs(path string, funcs map[string]interface{}) (Template, error) {
	return tmpl{
		path:  path,
		funcs: funcs,
	}, nil
}

type resource struct {
	path string
}

func newResource(path string) (Resource, error) {
	return resource{path}, nil
}

func (r resource) Get() (string, string, error) {
	return loadResource(r.path)
}
