// +build !development

package templates

//go:generate go-bindata -pkg $GOPACKAGE -ignore .*\.go -nometadata -o assets.go ./...

func create(path string) (Template, error) {
	return loadTemplate(path, make(map[string]interface{}))
}

func createWithFuncs(path string, funcs map[string]interface{}) (Template, error) {
	return loadTemplate(path, funcs)
}

type resource struct {
	data string
	etag string
}

func newResource(path string) (Resource, error) {
	data, etag, err := loadResource(path)
	if err != nil {
		return nil, err
	}
	return resource{data, etag}, nil
}

func (r resource) Get() (string, string, error) {
	return r.data, r.etag, nil
}
