package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/client"
)

type tmpPiece struct {
	Data []byte
}

func newTmpPiece(pieceLength int64) (t *tmpPiece) {
	return &tmpPiece{
		Data: make([]byte, pieceLength),
	}
}

type pieceInCache struct {
	buffer bytes.Reader
}

func (t *tmpPiece) WriteAt(p []byte, off int64) (n int, err error) {
	n, err = io.ReadFull(bytes.NewReader(p), t.Data[int(off):int(off)+len(p)])
	return
}

type blobStore struct {

	// The camlistore client
	client *client.Client

	// The pieces to be kept written to camlistore once they are correct
	inMemChunks map[string]*tmpPiece

	// A slice giving the offset of a piece
	pieceOffsets []string

	// The piece length for this torrent
	pieceLength int64

	// The total length of the data
	totalLength int64

	cachedPieces map[string]io.ReadSeeker
}

func NewBlobStore(info *InfoDict, uri string) (b *blobStore, totalLength int64, err error) {
	totalLength = info.Length
	err = nil
	numPieces := (info.Length + info.PieceLength - 1) / info.PieceLength
	pieceOffsets := make([]string, numPieces)
	for i := int64(0); i < numPieces; i++ {
		pieceOffsets[i] = info.Pieces[i*sha1.Size : (i+1)*sha1.Size]
	}

	b = &blobStore{
		client:       client.NewOrFail(),
		inMemChunks:  make(map[string]*tmpPiece),
		pieceOffsets: pieceOffsets,
		pieceLength:  info.PieceLength,
		totalLength:  totalLength,
		cachedPieces: make(map[string]io.ReadSeeker),
	}

	return
}

func (b *blobStore) ReadAt(p []byte, off int64) (n int, err error) {
	var divisor big.Int
	var m big.Int
	indexBig, beginBig := divisor.DivMod(big.NewInt(off), big.NewInt(b.pieceLength), &m)
	index := indexBig.Int64()
	begin := beginBig.Int64()
	pieceHash := b.pieceOffsets[index]

  /*
	piece := b.inMemChunks[pieceHash]

	if piece == nil {

		blobRef := blobrefFromHexhash(pieceHash)
		readCloser, thisPieceLength, err := b.client.FetchStreaming(blobRef)
		if err != nil {
			return 0, err
		}
		defer readCloser.Close()

		piece = newTmpPiece(thisPieceLength)
		_, err = io.ReadFull(readCloser, piece.Data)
		if err != nil {
			return 0, err
		}

		b.inMemChunks[pieceHash] = piece
	}

	n = copy(p, piece.Data[begin:begin+len(p)])
  */

  pieceReader, ok := b.cachedPieces[pieceHash]
  if !ok {
    blobRef := blobrefFromHexhash(pieceHash)
    readCloser, _, _ := b.client.FetchStreaming(blobRef)
    var buf bytes.Buffer
    io.Copy(&buf, readCloser)

    b.cachedPieces[pieceHash] = buf
  }

  n, err = pieceReader.ReadAt(p, begin)

	// At this point if there's anything left to read it means we've run off the
	// end of the file store. Read zeros. This is defined by the bittorrent protocol.
	for i, _ := range p[n:] {
		p[i] = 0
	}

	return
}

func (b *blobStore) WriteAt(p []byte, off int64) (n int, err error) {
	// TODO undo the logic done in caller where we abstract pieceLength,
	// because we mind about it here

	var divisor big.Int
	var m big.Int
	indexDiv, beginDiv := divisor.DivMod(big.NewInt(off), big.NewInt(b.pieceLength), &m)
	index := indexDiv.Int64()
	begin := beginDiv.Int64()
	pieceHash := b.pieceOffsets[index]
	piece := b.inMemChunks[pieceHash]
	if piece == nil {

		if index == int64(len(b.pieceOffsets))-1 {
			// last piece - data has a different size
			var lastPieceSizeBig big.Int
			var divisor big.Int
			divisor.DivMod(big.NewInt(b.totalLength), big.NewInt(b.pieceLength), &lastPieceSizeBig)
			piece = newTmpPiece(lastPieceSizeBig.Int64())
		} else {
			piece = newTmpPiece(b.pieceLength)
		}

		b.inMemChunks[pieceHash] = piece
	}

	n, err = piece.WriteAt(p, begin)
	if err != nil {
		return
	}

	if n < len(p) {
		// At this point if there's anything left to write it means we've run off the
		// end of the file store. Check that the data is zeros.
		// This is defined by the bittorrent protocol.
		for i, _ := range p[n:] {
			if p[i] != 0 {
				err = errors.New("Unexpected non-zero data at end of store.")
				n = n + i
				return
			}
		}
	}

	return
}

func (b *blobStore) Close() (err error) {
	// Nothing to do
	return
}

func (b *blobStore) CanVerifyPieces() (canVerify bool) {
	return true
}

func (b *blobStore) VerifyPieces(sha1s []string) (verified map[string]bool, err error) {
	ret := make(map[string]bool)

	// verify tmp pieces first
	for _, hash := range sha1s {
		if b.inMemChunks[hash] != nil {
			tmpPiece := b.inMemChunks[hash]

			tmpBlobRef := blobref.SHA1FromBytes(tmpPiece.Data)
			if tmpBlobRef.Digest() == fmt.Sprintf("%x", hash) {
				newBlobRef, err := b.client.ReceiveBlob(tmpBlobRef, bytes.NewReader(tmpPiece.Data))
				if err != nil {
					ret[hash] = false
					return ret, err
				}
				if !newBlobRef.BlobRef.Equal(tmpBlobRef) {
					ret[hash] = false
					return ret, errors.New(fmt.Sprintf("Expected blobref %s, got blobref %s", newBlobRef.String(), tmpBlobRef.String()))
				}

				delete(b.inMemChunks, hash)
				ret[hash] = true
			} else {
				log.Printf(fmt.Sprintf("Mismatch: torrent says %s, we have %s", fmt.Sprintf("%x", hash), tmpBlobRef.Digest()))
				delete(b.inMemChunks, hash)
			}
		}
	}

	if len(ret) == len(sha1s) {
		return ret, nil
	}

	existing := make(chan blobref.SizedBlobRef)
	blobsRequest := make([]*blobref.BlobRef, len(sha1s))
	for i, hexhash := range sha1s {
		blobsRequest[i] = blobrefFromHexhash(hexhash)
	}

	result := make(chan map[string]bool)
	stopCollecting := make(chan bool)
	go func() {
	collect:
		for {
			select {
			case exist := <-existing:
				decoded, _ := hex.DecodeString(exist.Digest())
				ret[string(decoded)] = true
			case <-stopCollecting:
				break collect
			}
		}

		result <- ret
	}()

	err = b.client.StatBlobs(existing, blobsRequest, 0)
	if err != nil {
		return
	}

	stopCollecting <- true
	ret = <-result

	for _, hash := range sha1s {
		if ret[hash] != true {
			ret[hash] = false
		}
	}

	return ret, nil
}

func blobrefFromHexhash(hexhash string) *blobref.BlobRef {
	return blobref.MustParse("sha1-" + fmt.Sprintf("%x", hexhash))
}
