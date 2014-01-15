package main

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"
)

type LpdAnnounce struct {
	peer     string
	infohash string
}

type LpdAnnouncer struct {
	btPort  int
	lpdAddr *net.UDPAddr
	lpdConn *net.UDPConn
}

func NewLpdAnnouncer(listenPort int) (lpd *LpdAnnouncer) {
	lpdAddr, err := net.ResolveUDPAddr("udp4", "239.192.152.143:6771")
	if err != nil {
		return
	}

	lpdConn, err := net.ListenMulticastUDP("udp4", nil, lpdAddr)
	if err != nil {
		return
	}

	return &LpdAnnouncer{listenPort, lpdAddr, lpdConn}
}

func (lpd *LpdAnnouncer) listenPeerDiscoveries() (announces chan *LpdAnnounce, err error) {
	announces = make(chan *LpdAnnounce)

	go func() {
		for _ = range time.Tick(1 * time.Second) {

			answer := make([]byte, 256)
			_, from, err := lpd.lpdConn.ReadFromUDP(answer)
			req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(answer)))
			if err != nil {
				log.Println(err)
				continue
			}

			if req.Method != "BT-SEARCH" {
				log.Println("Invalid method: ", req.Method)
			}

			ih := req.Header.Get("Infohash")
			if ih == "" {
				log.Println("No Infohash")
				continue
			}

			port := req.Header.Get("Port")
			if port == "" {
				log.Println("No port")
				continue
			}

			addr, err := net.ResolveTCPAddr("tcp4", from.IP.String()+":"+port)
			if err != nil {
				log.Println(err)
				continue
			}
			announces <- &LpdAnnounce{addr.String(), ih}
		}
	}()

	return
}

func (lpd *LpdAnnouncer) registerForLPD(ih string) {
	var requestMessage bytes.Buffer
	fmt.Fprintln(&requestMessage, "BT-SEARCH * HTTP/1.1")
	fmt.Fprintln(&requestMessage, "Host: 239.192.152.143:6771")
	fmt.Fprintf(&requestMessage, "Port: %d\r\n", lpd.btPort)
	fmt.Fprintf(&requestMessage, "Infohash: %x\r\n", ih)
	fmt.Fprintln(&requestMessage, "")
	fmt.Fprintln(&requestMessage, "")

	go func() {
		// Announce at launch, then every 5 minutes
		_, err := lpd.lpdConn.WriteToUDP(requestMessage.Bytes(), lpd.lpdAddr)
		if err != nil {
			log.Println(err)
		}

		for _ = range time.Tick(5 * time.Minute) {
			_, err := lpd.lpdConn.WriteToUDP(requestMessage.Bytes(), lpd.lpdAddr)
			if err != nil {
				log.Println(err)
			}
		}
	}()
}
