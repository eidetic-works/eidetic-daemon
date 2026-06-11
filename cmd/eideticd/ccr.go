package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/eidetic-works/eidetic-daemon/internal/ccr"
)

func runCCR(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: eideticd ccr serve [flags]")
		os.Exit(1)
	}

	sub := args[0]
	switch sub {
	case "serve":
		runCCRServe(args[1:])
	default:
		log.Fatalf("ccr: unknown subcommand %q", sub)
	}
}

func runCCRServe(args []string) {
	fs := flag.NewFlagSet("ccr serve", flag.ExitOnError)
	mcpStdio := fs.Bool("mcp-stdio", false, "run MCP server over stdio")
	unixSocket := fs.Bool("unix-socket", false, "listen on unix socket")
	webSocket := fs.Bool("websocket", false, "listen for websocket connections")
	fs.Parse(args)

	ccr.Serve(&ccr.ServeOptions{
		MCPStdio:   *mcpStdio,
		UnixSocket: *unixSocket,
		WebSocket:  *webSocket,
	})
}
