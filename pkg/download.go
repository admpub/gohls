package pkg

import (
	"context"
	"log"
	"time"
)

type Config struct {
	PlaylistURL  string
	OutputFile   string
	Duration     time.Duration
	UseLocalTime bool
	Total        int
	Finished     int
}

func (cfg *Config) Get(ctx context.Context) error {
	return Get(ctx, cfg)
}

func Get(ctx context.Context, cfg *Config) error {
	msChan := make(chan *Download, 1024)
	done := make(chan error)
	closeChan := func() {
		close(msChan)
		close(done)
	}
	var err error
	go func() {
		err = GetPlaylist(cfg.PlaylistURL, cfg.Duration, cfg.UseLocalTime, msChan, &cfg.Total)
		if err != nil {
			log.Println(err)
			closeChan()
		}
	}()
	go func() {
		done <- DownloadSegment(cfg.OutputFile, msChan, cfg.Duration, &cfg.Finished)
	}()
	for {
		select {
		case <-ctx.Done():
			closeChan()
			return nil
		case err = <-done:
			return err
		}
	}
}
