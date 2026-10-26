// Command beacon pings services and reports their latency.
package main

import (
	"flag"
	"os"
	"time"
)

type options struct {
	targets  string
	interval time.Duration
	timeout  time.Duration
	count    int
	jsonOut  bool
	quiet    bool
	failFast bool
	header   string
}

func parseFlags() options {
	var o options
	fs := flag.NewFlagSet("beacon", flag.ExitOnError)
	fs.StringVar(&o.targets, "targets", "targets.txt", "file with one URL per line")
	fs.DurationVar(&o.interval, "interval", 30*time.Second, "time between rounds")
	fs.DurationVar(&o.timeout, "timeout", 5*time.Second, "time to wait for each answer")
	fs.IntVar(&o.count, "count", 0, "rounds to run (0 runs until stopped)")
	fs.BoolVar(&o.jsonOut, "json", false, "print results as JSON lines")
	fs.BoolVar(&o.quiet, "quiet", false, "print failures only")
	fs.BoolVar(&o.failFast, "fail-fast", false, "stop at the first failure")
	fs.StringVar(&o.header, "header", "", "extra request header, as Name: value")
	fs.Parse(os.Args[1:])
	return o
}
