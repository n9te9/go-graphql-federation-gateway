package federation

type SubGraph struct {
	Name         string
	SDL          string
	Host         string
	IsIntegrated bool
}

func NewSubGraph(name string, host, sdl string) *SubGraph {
	return &SubGraph{
		Name: name,
		Host: host,
		SDL:  sdl,
	}
}
