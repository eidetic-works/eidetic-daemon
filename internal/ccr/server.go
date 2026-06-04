package ccr

import (
	"log"
	"sync"
	"time"
)

type ServeOptions struct {
	MCPStdio   bool
	UnixSocket bool
	WebSocket  bool
}

func Serve(opts *ServeOptions) {
	log.Println("ccr serve: starting Coordination Runtime")

	var wg sync.WaitGroup
	broker := NewBroker(30 * time.Second)

	wg.Add(1)
	go func() {
		defer wg.Done()
		RunRelayWatcher(broker, "")
	}()

	if opts.MCPStdio {
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Println("ccr serve: starting MCP server on stdio")
			RunMCPServer(broker)
		}()
	}

	if opts.UnixSocket {
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Println("ccr serve: starting Unix socket server")
			RunUnixServer("", broker)
		}()
	}

	if opts.WebSocket {
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Println("ccr serve: starting WebSocket server")
			RunWebSocketServer(broker, "")
		}()
	}

	wg.Wait()
}
