package shared

import (
	"net/rpc"

	"github.com/bwmarrin/discordgo"
	"github.com/hashicorp/go-plugin"
)

// GBP is the interface that we're exposing as a plugin.
type GBP interface {
	Start(*discordgo.Session) string
}

// Here is an implementation that talks over RPC
type GBPRPC struct{ client *rpc.Client }

func (g *GBPRPC) Start() string {
	var resp string
	err := g.client.Call("Plugin.Greet", new(interface{}), &resp)
	if err != nil {
		// You usually want your interfaces to return errors. If they don't,
		// there isn't much other choice here.
		panic(err)
	}

	return resp
}

// Here is the RPC server that GBPRPC talks to, conforming to
// the requirements of net/rpc
type GBPRPCServer struct {
	// This is the real implementation
	Impl GBP
}

func (s *GBPRPCServer) Start(args interface{}, resp *string) error {
	*resp = s.Impl.Start(args * discordgo.Session)
	return nil
}

// This is the implementation of plugin.Plugin so we can serve/consume this
//
// This has two methods: Server must return an RPC server for this plugin
// type. We construct a GBPRPCServer for this.
//
// Client must return an implementation of our interface that communicates
// over an RPC client. We return GBPRPC for this.
//
// Ignore MuxBroker. That is used to create more multiplexed streams on our
// plugin connection and is a more advanced use case.
type GBPPlugin struct {
	// Impl Injection
	Impl GBP
}

func (p *GBPPlugin) Server(*plugin.MuxBroker) (interface{}, error) {
	return &GBPRPCServer{Impl: p.Impl}, nil
}

func (GBPPlugin) Client(b *plugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	return &GBPRPC{client: c}, nil
}
