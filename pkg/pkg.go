package pkg

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
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
	UserAgent     = "Mozilla/5.0 (X11; Linux x86_64; rv:38.0) Gecko/38.0 Firefox/38.0"
	Client        = &http.Client{}
	IVPlaceholder = []byte{0, 0, 0, 0, 0, 0, 0, 0}
)

func DoRequest(c *http.Client, req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", UserAgent)
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

		if len(v.ExtXKey.IV) == 0 {
			iv = bytes.NewBuffer(IVPlaceholder)
			binary.Write(iv, binary.BigEndian, v.SeqNo)
		} else {
			iv = bytes.NewBufferString(v.ExtXKey.IV)
		}

		cipherBlock, _ = aes.NewCipher((*aes128Keys)[v.ExtXKey.URI])
		cipher.NewCBCDecrypter(cipherBlock, iv.Bytes()).CryptBlocks(data, data)
	}
	return nil
}

func DownloadSegment(fn string, dlc chan *Download, recTime time.Duration, finished *int) error {
	var out, err = os.Create(fn)
	defer out.Close()

	if err != nil {
		return err
	}
	var (
		data       []byte
		aes128Keys = &map[string][]byte{}
	)

	defer func() {
		if e := recover(); e != nil {
			log.Println(e)
		}
	}()
	for v := range dlc {
		(*finished)++
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

func IsFullURL(url string) bool {
	if len(url) < 8 {
		return false
	}
	switch strings.ToLower(url[0:7]) {
	case `https:/`, `http://`:
		return true
	default:
		return false
	}
}

func ParseURI(root *url.URL, uri string) (string, error) {
	msURI, err := url.QueryUnescape(uri)
	if err != nil {
		return msURI, err
	}
	if !IsFullURL(msURI) {
		msURL, err := root.Parse(msURI)
		if err != nil {
			return msURI, err
		}
		msURI, err = url.QueryUnescape(msURL.String())
	}
	return msURI, err
}

func GetPlaylist(urlStr string, recTime time.Duration, useLocalTime bool, dlc chan *Download, total *int) error {
	startTime := time.Now()
	var recDuration time.Duration
	cache := lru.New(1024)
	defer func() {
		if e := recover(); e != nil {
			log.Println(e)
		}
		cache.Clear()
	}()
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
			if listType == m3u8.MASTER {
				mpl := playlist.(*m3u8.MasterPlaylist)
				for _, v := range mpl.Variants {
					if v == nil {
						continue
					}
					msURI, err := ParseURI(playlistURL, v.URI)
					if err != nil {
						log.Println(err)
						continue
					}
					return GetPlaylist(msURI, recTime, useLocalTime, dlc, total)
				}
				return ErrInvalidMasterPlaylist
			}
			return ErrInvalidMediaPlaylist
		}
		mpl := playlist.(*m3u8.MediaPlaylist)
		*total = len(mpl.Segments)
		for segmentIndex, v := range mpl.Segments {
			if v == nil {
				continue
			}
			msURI, err := ParseURI(playlistURL, v.URI)
			if err != nil {
				log.Println(err)
				continue
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
		if mpl.Closed {
			close(dlc)
			return nil
		}
		time.Sleep(time.Duration(int64(mpl.TargetDuration * 1000000000)))
	}
}
