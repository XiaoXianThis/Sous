package acp

import (
	"context"
	"encoding/json"
	"fmt"
)

// LegacyModelOption is the id given to the model selector of agents that
// report the older, unstable "models" field instead of configOptions. It is
// set with session/set_model.
const LegacyModelOption = "_model"

// ConfigOption is a session config option (ACP "Session Config Options"):
// a selector for a model, a reasoning level, a mode, and so on. Only
// select options are kept; other types are ignored, as the protocol asks.
type ConfigOption struct {
	ID          string
	Name        string
	Description string
	Category    string // mode / model / model_config / thought_level / _custom
	Current     string
	Values      []ConfigValue
}

type ConfigValue struct {
	Value       string
	Name        string
	Description string
	Group       string // the group's name, for grouped selects
}

// Has reports whether v is one of the option's values.
func (o ConfigOption) Has(v string) bool {
	for _, x := range o.Values {
		if x.Value == v {
			return true
		}
	}
	return false
}

type rawSelectValue struct {
	Value       string            `json:"value"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Group       string            `json:"group"`
	Options     []json.RawMessage `json:"options"`
}

// ParseConfigOptions reads configOptions, skipping what it does not understand.
func ParseConfigOptions(raw []json.RawMessage) []ConfigOption {
	var out []ConfigOption
	for _, r := range raw {
		var o struct {
			ID           string            `json:"id"`
			Name         string            `json:"name"`
			Description  string            `json:"description"`
			Category     string            `json:"category"`
			Type         string            `json:"type"`
			CurrentValue json.RawMessage   `json:"currentValue"`
			Options      []json.RawMessage `json:"options"`
		}
		if json.Unmarshal(r, &o) != nil || o.ID == "" || (o.Type != "" && o.Type != "select") {
			continue
		}
		opt := ConfigOption{ID: o.ID, Name: o.Name, Description: o.Description, Category: o.Category}
		json.Unmarshal(o.CurrentValue, &opt.Current)
		for _, v := range o.Options {
			var sv rawSelectValue
			if json.Unmarshal(v, &sv) != nil {
				continue
			}
			if sv.Group != "" || len(sv.Options) > 0 {
				for _, gv := range sv.Options {
					var x rawSelectValue
					if json.Unmarshal(gv, &x) == nil && x.Value != "" {
						opt.Values = append(opt.Values, ConfigValue{Value: x.Value, Name: x.Name, Description: x.Description, Group: sv.Name})
					}
				}
				continue
			}
			if sv.Value != "" {
				opt.Values = append(opt.Values, ConfigValue{Value: sv.Value, Name: sv.Name, Description: sv.Description})
			}
		}
		if opt.Name == "" {
			opt.Name = opt.ID
		}
		out = append(out, opt)
	}
	return out
}

// SetConfigOption changes one of the session's options and returns the
// options as the agent reports them afterwards (unchanged list for the
// legacy model selector).
func (c *Client) SetConfigOption(ctx context.Context, s Session, id, value string) ([]ConfigOption, error) {
	if id == LegacyModelOption {
		if err := c.conn.Call(ctx, "session/set_model", map[string]string{"sessionId": s.ID, "modelId": value}, nil); err != nil {
			return nil, fmt.Errorf("session/set_model: %w", err)
		}
		out := append([]ConfigOption(nil), s.Options...)
		for i := range out {
			if out[i].ID == id {
				out[i].Current = value
			}
		}
		return out, nil
	}
	var res struct {
		ConfigOptions []json.RawMessage `json:"configOptions"`
	}
	err := c.conn.Call(ctx, "session/set_config_option", map[string]string{"sessionId": s.ID, "configId": id, "value": value}, &res)
	if err != nil {
		return nil, fmt.Errorf("session/set_config_option: %w", err)
	}
	if opts := ParseConfigOptions(res.ConfigOptions); len(opts) > 0 {
		return opts, nil
	}
	// Some agents answer {}: assume the change took.
	out := append([]ConfigOption(nil), s.Options...)
	for i := range out {
		if out[i].ID == id {
			out[i].Current = value
		}
	}
	return out, nil
}
