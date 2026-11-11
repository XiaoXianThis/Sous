// Package sysmap describes the Hub's modules and the lines between them,
// counts what flows along each line, and collects each module's status for
// the system map page (work/specs/system-map).
package sysmap

import "context"

type Layer string

const (
	LayerClient   Layer = "client"
	LayerHub      Layer = "hub"
	LayerExternal Layer = "external"
	LayerNode     Layer = "node"
)

type State string

const (
	OK       State = "ok"
	Busy     State = "busy"
	Idle     State = "idle"
	Degraded State = "degraded"
	Down     State = "down"
)

type Module struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Layer Layer  `json:"layer"`
	Desc  string `json:"desc"`
	Code  string `json:"code,omitempty"`
}

type Edge struct {
	ID    string `json:"id"` // "<from>><to>"
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label"`
	// Carries says in words what goes along the line, for the details panel.
	Carries string `json:"carries"`
}

type Gauge struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
	Unit  string  `json:"unit,omitempty"`
}

type Item struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
	State  State  `json:"state"`
	// Link is a hash route of the PWA, such as "#/task=<id>".
	Link string `json:"link,omitempty"`
}

type Status struct {
	State   State   `json:"state"`
	Summary string  `json:"summary"`
	Gauges  []Gauge `json:"gauges,omitempty"`
	Items   []Item  `json:"items,omitempty"`
}

// Prober reports a module's status. Probe must return within a few
// milliseconds: read memory, or a store query cached for 2 seconds.
type Prober interface {
	Probe(ctx context.Context) Status
}

type ProbeFunc func(ctx context.Context) Status

func (f ProbeFunc) Probe(ctx context.Context) Status { return f(ctx) }

// Instance is one module that exists at run time: a model provider, an
// MCP server, a machine.
type Instance struct {
	Module Module
	Status Status
}
