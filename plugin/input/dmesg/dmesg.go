//go:build linux
// +build linux

package dmesg

import (
	"time"

	"github.com/euank/go-kmsg-parser/kmsgparser"
	"github.com/ozonru/file.d/fd"
	"github.com/ozonru/file.d/longpanic"
	"github.com/ozonru/file.d/offset"
	"github.com/ozonru/file.d/pipeline"
	insaneJSON "github.com/vitkovskii/insane-json"
	"go.uber.org/zap"
)

/*{ introduction
It reads kernel events from /dev/kmsg
}*/

type Plugin struct {
	config     *Config
	state      *state
	controller pipeline.InputPluginController
	parser     kmsgparser.Parser
	logger     *zap.SugaredLogger
}

//! config-params
//^ config-params
type Config struct {
	//> @3@4@5@6
	//>
	//> The filename to store offsets of processed messages.
	//> > It's a `json` file. You can modify it manually.
	OffsetsFile string `json:"offsets_file" required:"true"` //*
}

type state struct {
	TS int64 `json:"ts"`
}

func init() {
	fd.DefaultPluginRegistry.RegisterInput(&pipeline.PluginStaticInfo{
		Type:    "dmesg",
		Factory: Factory,
	})
}

func Factory() (pipeline.AnyPlugin, pipeline.AnyConfig) {
	return &Plugin{}, &Config{}
}

func (p *Plugin) Start(config pipeline.AnyConfig, params *pipeline.InputPluginParams) {
	p.logger = params.Logger
	p.config = config.(*Config)
	p.controller = params.Controller

	p.state = &state{}
	if err := offset.LoadYAML(p.config.OffsetsFile, p.state); err != nil {
		p.logger.Error("can't load offset file: %s", err.Error())
	}

	parser, err := kmsgparser.NewParser()
	if err != nil {
		p.logger.Fatalf("can't create kmsg parser: %s", err.Error())
	}

	p.parser = parser

	longpanic.Go(p.read)
}

func (p *Plugin) read() {
	root := insaneJSON.Spawn()
	out := make([]byte, 0)
	for m := range p.parser.Parse() {
		ts := m.Timestamp.UnixNano()
		if ts <= p.state.TS {
			continue
		}

		level := "debug"
		switch m.Priority {
		case 0, 1, 2, 3:
			level = "error"
		case 4, 5:
			level = "warn"
		case 6:
			level = "info"
		}

		root.AddFieldNoAlloc(root, "level").MutateToString(level)
		root.AddFieldNoAlloc(root, "ts").MutateToString(m.Timestamp.Format(time.RFC3339))
		root.AddFieldNoAlloc(root, "priority").MutateToInt(m.Priority)
		root.AddFieldNoAlloc(root, "sequence_number").MutateToInt(m.SequenceNumber)
		root.AddFieldNoAlloc(root, "message").MutateToString(m.Message)

		out = root.Encode(out[:0])

		p.controller.In(0, "", ts, out, false)
	}
}

func (p *Plugin) Stop() {
}

func (p *Plugin) Commit(event *pipeline.Event) {
	p.state.TS = event.Offset

	if err := offset.SaveYAML(p.config.OffsetsFile, p.state); err != nil {
		p.logger.Error("can't save offset file: %s", err.Error())
	}
}
