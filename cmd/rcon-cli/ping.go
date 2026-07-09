package main

import (
	"os"
	"time"

	"github.com/simon7073/hll-rcon-client/core"
	"github.com/simon7073/hll-rcon-client/rcon"
)

// cmdPing tests connectivity to the RCON server.
// It connects, performs the handshake, and reports timing.
func cmdPing(p *Printer, host, port, password string, timeout time.Duration, dialOpts ...core.DialOption) {
	start := time.Now()
	client := rcon.NewClient(host, port, password, dialOpts...)
	err := client.Connect(timeout)
	elapsed := time.Since(start).Round(time.Millisecond)
	client.Close() // ping is one-shot — always close

	if p.format == FmtJSON {
		r := JSONOutput{OK: err == nil, Elapsed: elapsed.String()}
		if err != nil {
			r.Error = err.Error()
		} else {
			r.Data = map[string]string{"addr": client.Addr()}
		}
		p.JSON(r)
		if err != nil {
			os.Exit(1)
		}
		return
	}

	if err != nil {
		p.Printf("  %s  %s %s %s\n",
			p.redBold("FAIL"),
			p.dim("("+elapsed.String()+")"),
			p.cyan(client.Addr()),
			p.red(err.Error()))
		os.Exit(1)
	}

	p.Printf("  %s  %s %s\n",
		p.greenBold("OK"),
		p.dim("("+elapsed.String()+")"),
		p.cyan(client.Addr()))
}
