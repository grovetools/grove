package project

import "fmt"

type Registry struct {
	handlers map[Type]ProjectHandler
}

func NewRegistry() *Registry {
	r := &Registry{
		handlers: make(map[Type]ProjectHandler),
	}
	
	// Register default handlers
	r.Register(TypeGo, NewGoHandler())
	r.Register(TypeMaturin, NewMaturinHandler())
	r.Register(TypeNode, NewNodeHandler())
	r.Register(TypeTemplate, NewTemplateHandler())
	
	return r
}

func (r *Registry) Register(projectType Type, handler ProjectHandler) {
	r.handlers[projectType] = handler
}

func (r *Registry) Get(projectType Type) (ProjectHandler, error) {
	handler, ok := r.handlers[projectType]
	if !ok {
		return nil, fmt.Errorf("no handler for project type: %s", projectType)
	}
	return handler, nil
}