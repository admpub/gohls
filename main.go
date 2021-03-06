/*

   This program is free software: you can redistribute it and/or modify
   it under the terms of the GNU General Public License as published by
   the Free Software Foundation, either version 3 of the License, or
   (at your option) any later version.

   This program is distributed in the hope that it will be useful,
   but WITHOUT ANY WARRANTY; without even the implied warranty of
   MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
   GNU General Public License for more details.

   You should have received a copy of the GNU General Public License
   along with this program.  If not, see <http://www.gnu.org/licenses/>.

*/

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/admpub/gohls/pkg"
)

const VERSION = "1.0.7"

func main() {

	duration := flag.Duration("t", time.Duration(0), "Recording duration (0 == infinite)")
	useLocalTime := flag.Bool("l", false, "Use local time to track duration instead of supplied metadata")
	flag.StringVar(&pkg.UserAgent, "ua", fmt.Sprintf("gohls/%v", VERSION), "User-Agent for HTTP Client")
	flag.Parse()

	os.Stderr.Write([]byte(fmt.Sprintf("gohls %v - HTTP Live Streaming (HLS) downloader\n", VERSION)))
	os.Stderr.Write([]byte("Copyright (C) 2013 GoHLS Authors. Licensed for use under the GNU GPL version 3.\n"))

	if flag.NArg() < 2 {
		os.Stderr.Write([]byte("Usage: gohls [-l=bool] [-t duration] [-ua user-agent] media-playlist-url output-file.ts\n"))
		flag.PrintDefaults()
		os.Exit(2)
	}

	if !pkg.IsFullURL(flag.Arg(0)) {
		log.Fatal("Media playlist url must begin with http/https")
	}
	cfg := &pkg.Config{
		PlaylistURL:  flag.Arg(0),
		OutputFile:   flag.Arg(1),
		Duration:     *duration,
		UseLocalTime: *useLocalTime,
	}
	err := cfg.Get(context.Background())
	if err != nil {
		log.Fatal(err)
	}
}
