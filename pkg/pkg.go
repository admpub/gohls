package pkg

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/golang/groupcache/lru"
	"github.com/grafov/m3u8"
)

var (
	USER_AGENT     = "Mozilla/5.0 (X11; Linux x86_64; rv:38.0) Gecko/38.0 Firefox/38.0"
	Client         = &http.Client{}
	IV_placeholder = []byte{0, 0, 0, 0, 0, 0, 0, 0}
)

func DoRequest(c *http.Client, req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", USER_AGENT)
	//req.Header.Set("Connection", "Keep-Alive") //http2不支持Keep-Alive
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	// Maybe in the future it will force connection to stay opened for "Connection: close"
	resp.Close = false
	resp.Request.Close = false

	return resp, err
}

type Download struct {
	URI           string
	SeqNo         uint64
	ExtXKey       *m3u8.Key
	totalDuration time.Duration
}

func DecryptData(data []byte, v *Download, aes128Keys *map[string][]byte) error {
	var (
		iv          *bytes.Buffer
		keyData     []byte
		cipherBlock cipher.Block
	)

	if v.ExtXKey != nil && (v.ExtXKey.Method == "AES-128" || v.ExtXKey.Method == "aes-128") {

		keyData = (*aes128Keys)[v.ExtXKey.URI]

		if keyData == nil {
			req, err := http.NewRequest("GET", v.ExtXKey.URI, nil)
			if err != nil {
				log.Println(err)
			}
			resp, err := DoRequest(Client, req)
			if err != nil {
				log.Println(err)
			}
			keyData, err = ioutil.ReadAll(resp.Body)
			if err != nil {
				log.Println(err)
			}
			resp.Body.Close()
			(*aes128Keys)[v.ExtXKey.URI] = keyData
		}

		if v.ExtXKey.IV == "" {
			iv = bytes.NewBuffer(IV_placeholder)
			binary.Write(iv, binary.BigEndian, v.SeqNo)
		} else {
			iv = bytes.NewBufferString(v.ExtXKey.IV)
		}

		cipherBlock, _ = aes.NewCipher((*aes128Keys)[v.ExtXKey.URI])
		cipher.NewCBCDecrypter(cipherBlock, iv.Bytes()).CryptBlocks(data, data)
	}
	return nil
}

func DownloadSegment(fn string, dlc chan *Download, recTime time.Duration) error {
	var out, err = os.Create(fn)
	defer out.Close()

	if err != nil {
		return err
	}
	var (
		data       []byte
		aes128Keys = &map[string][]byte{}
	)

	for v := range dlc {
		req, err := http.NewRequest("GET", v.URI, nil)
		if err != nil {
			return err
		}
		resp, err := DoRequest(Client, req)
		if err != nil {
			log.Print(err)
			continue
		}
		if resp.StatusCode != 200 {
			log.Printf("Received HTTP %v for %v\n", resp.StatusCode, v.URI)
			resp.Body.Close()
			continue
		}

		data, _ = ioutil.ReadAll(resp.Body)
		resp.Body.Close()

		DecryptData(data, v, aes128Keys)

		_, err = out.Write(data)
		// _, err = io.Copy(out, resp.Body)
		if err != nil {
			return err
		}

		log.Printf("Downloaded %v\n", v.URI)
		if recTime != 0 {
			log.Printf("Recorded %v of %v\n", v.totalDuration, recTime)
		} else {
			log.Printf("Recorded %v\n", v.totalDuration)
		}
	}
	return nil
}

func GetPlaylist(urlStr string, recTime time.Duration, useLocalTime bool, dlc chan *Download) error {
	startTime := time.Now()
	var recDuration time.Duration
	cache := lru.New(1024)
	playlistURL, err := url.Parse(urlStr)
	if err != nil {
		return err
	}
	for {
		req, err := http.NewRequest("GET", urlStr, nil)
		if err != nil {
			return err
		}
		resp, err := DoRequest(Client, req)
		if err != nil {
			log.Println(err)
			time.Sleep(time.Duration(3) * time.Second)
		}
		playlist, listType, err := m3u8.DecodeFrom(resp.Body, true)
		if err != nil {
			resp.Body.Close()
			return err
		}
		resp.Body.Close()
		if listType != m3u8.MEDIA {
			return errors.New("Not a valid media playlist")
		}
		mpl := playlist.(*m3u8.MediaPlaylist)

		for segmentIndex, v := range mpl.Segments {
			if v != nil {
				var msURI string
				if strings.HasPrefix(v.URI, "http") {
					msURI, err = url.QueryUnescape(v.URI)
					if err != nil {
						return err
					}
				} else {
					msURL, err := playlistURL.Parse(v.URI)
					if err != nil {
						log.Print(err)
						continue
					}
					msURI, err = url.QueryUnescape(msURL.String())
					if err != nil {
						return err
					}
				}
				_, hit := cache.Get(msURI)
				if !hit {
					cache.Add(msURI, nil)
					if useLocalTime {
						recDuration = time.Now().Sub(startTime)
					} else {
						recDuration += time.Duration(int64(v.Duration * 1000000000))
					}
					dlc <- &Download{
						URI:           msURI,
						ExtXKey:       mpl.Key,
						SeqNo:         uint64(segmentIndex) + mpl.SeqNo,
						totalDuration: recDuration,
					}
				}
				if recTime != 0 && recDuration != 0 && recDuration >= recTime {
					close(dlc)
					return nil
				}
			}
		}
		if mpl.Closed {
			close(dlc)
			return nil
		}
		time.Sleep(time.Duration(int64(mpl.TargetDuration * 1000000000)))
	}
}
