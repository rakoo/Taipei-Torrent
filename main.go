package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"runtime/pprof"

	"camlistore.org/pkg/client"
)

var torrent string

func main() {
	flag.Usage = usage
	flag.Parse()

	args := flag.Args()
	narg := flag.NArg()
	if narg != 1 {
		if narg < 1 {
			log.Println("Too few arguments. Torrent file or torrent URL required.")
		} else {
			log.Printf("Too many arguments. (Expected 1): %v", args)
		}
		usage()
	}

	// Add flag overriding camlistore settings if needed
	client.AddFlags()

	torrent = args[0]

	f, err := os.Create("pprof.log")
	if err != nil {
		log.Fatal(err)
	}
	pprof.StartCPUProfile(f)
	defer pprof.StopCPUProfile()

	interruptChan := make(chan os.Signal, 1)
	signal.Notify(interruptChan, os.Interrupt)

	log.Println("Starting.")
	ts, err := NewTorrentSession(torrent)
	if err != nil {
		log.Println("Could not create torrent session.", err)
		return
	}

	stopChan := make(chan bool)
	go func() {
		select {
		case <-interruptChan:
			stopChan <- true
		}
	}()

	err = ts.DoTorrent(stopChan)
	if err != nil {
		log.Println("Failed: ", err)
	} else {
		log.Println("Done")
	}
}

func usage() {
	log.Printf("usage: Taipei-Torrent [options] (torrent-file | torrent-url)")

	flag.PrintDefaults()
	os.Exit(2)
}
