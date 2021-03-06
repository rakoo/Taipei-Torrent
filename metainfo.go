package main

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"io"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"time"

	bencode "code.google.com/p/bencode-go"
	"github.com/nictuku/dht"
)

type FileDict struct {
	Length int64
	Path   []string
	Md5sum string
}

type InfoDict struct {
	PieceLength int64 "piece length"
	Pieces      string
	Private     int64
	Name        string
	// Single File Mode
	Length int64
	Md5sum string
	// Multiple File mode
	Files []FileDict
}

type MetaInfo struct {
	Info         InfoDict
	InfoHash     string
	Announce     string
	AnnounceList [][]string "announce-list"
	CreationDate string     "creation date"
	Comment      string
	CreatedBy    string "created by"
	Encoding     string
}

func getString(m map[string]interface{}, k string) string {
	if v, ok := m[k]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// Parse a list of list of strings structure, filtering out anything that's
// not a string, and filtering out empty lists. May return nil.
func getSliceSliceString(m map[string]interface{}, k string) (aas [][]string) {
	if a, ok := m[k]; ok {
		if b, ok := a.([]interface{}); ok {
			for _, c := range b {
				if d, ok := c.([]interface{}); ok {
					var sliceOfStrings []string
					for _, e := range d {
						if f, ok := e.(string); ok {
							sliceOfStrings = append(sliceOfStrings, f)
						}
					}
					if len(sliceOfStrings) > 0 {
						aas = append(aas, sliceOfStrings)
					}
				}
			}
		}
	}
	return
}

func getMetaInfo(torrent string) (metaInfo *MetaInfo, err error) {
	var input io.ReadCloser
	if strings.HasPrefix(torrent, "http:") {
		r, err := proxyHttpGet(torrent)
		if err != nil {
			return nil, err
		}
		input = r.Body
	} else if strings.HasPrefix(torrent, "magnet:") {
		magnet, err := parseMagnet(torrent)
		if err != nil {
			log.Println("Couldn't parse magnet: ", err)
			return nil, err
		}

		ih, err := dht.DecodeInfoHash(magnet.InfoHashes[0])
		if err != nil {
			return nil, err
		}

		metaInfo = &MetaInfo{InfoHash: string(ih)}
		return metaInfo, err

	} else {
		if input, err = os.Open(torrent); err != nil {
			return
		}
	}

	// We need to calcuate the sha1 of the Info map, including every value in the
	// map. The easiest way to do this is to read the data using the Decode
	// API, and then pick through it manually.
	var m interface{}
	m, err = bencode.Decode(input)
	input.Close()
	if err != nil {
		err = errors.New("Couldn't parse torrent file phase 1: " + err.Error())
		return
	}

	topMap, ok := m.(map[string]interface{})
	if !ok {
		err = errors.New("Couldn't parse torrent file phase 2.")
		return
	}

	infoMap, ok := topMap["info"]
	if !ok {
		err = errors.New("Couldn't parse torrent file. info")
		return
	}
	var b bytes.Buffer
	if err = bencode.Marshal(&b, infoMap); err != nil {
		return
	}
	hash := sha1.New()
	hash.Write(b.Bytes())

	var m2 MetaInfo
	err = bencode.Unmarshal(&b, &m2.Info)
	if err != nil {
		return
	}

	m2.InfoHash = string(hash.Sum(nil))
	m2.Announce = getString(topMap, "announce")
	m2.AnnounceList = getSliceSliceString(topMap, "announce-list")
	m2.CreationDate = getString(topMap, "creation date")
	m2.Comment = getString(topMap, "comment")
	m2.CreatedBy = getString(topMap, "created by")
	m2.Encoding = getString(topMap, "encoding")

	metaInfo = &m2
	return
}

type TrackerResponse struct {
	FailureReason  string "failure reason"
	WarningMessage string "warning message"
	Interval       time.Duration
	MinInterval    time.Duration "min interval"
	TrackerId      string        "tracker id"
	Complete       int
	Incomplete     int
	Peers          string
	Peers6         string
}

type SessionInfo struct {
	PeerId     string
	Port       int
	Uploaded   int64
	Downloaded int64
	Left       int64

	UseDHT      bool
	FromMagnet  bool
	HaveTorrent bool

	OurExtensions map[int]string
	ME            *MetaDataExchange
}

type MetaDataExchange struct {
	Transferring bool
	Pieces       [][]byte
}

func getTrackerInfo(url string) (tr *TrackerResponse, err error) {
	r, err := proxyHttpGet(url)
	if err != nil {
		return
	}
	defer r.Body.Close()
	if r.StatusCode >= 400 {
		data, _ := ioutil.ReadAll(r.Body)
		reason := "Bad Request " + string(data)
		log.Println(reason)
		err = errors.New(reason)
		return
	}
	var tr2 TrackerResponse
	err = bencode.Unmarshal(r.Body, &tr2)
	r.Body.Close()
	if err != nil {
		return
	}
	tr = &tr2
	return
}

func saveMetaInfo(metadata string) (err error) {
	var info InfoDict
	err = bencode.Unmarshal(bytes.NewReader([]byte(metadata)), &info)
	if err != nil {
		return
	}

	f, err := os.Create(info.Name + ".torrent")
	if err != nil {
		log.Println("Error when opening file for creation: ", err)
		return
	}
	defer f.Close()

	_, err = f.WriteString(metadata)

	return
}
