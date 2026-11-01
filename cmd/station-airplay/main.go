package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	airplay "github.com/skrashevich/station-airplay"
)

var version = "dev"

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.LUTC)
	var cfg airplay.Config
	flag.StringVar(&cfg.Name, "name", "", "Name shown in AirPlay (default: hostname + AirPlay)")
	flag.StringVar(&cfg.Interface, "interface", "", "Network interface (automatic by default)")
	flag.StringVar(&cfg.ListenAddress, "listen", ":7000", "IPv4 RTSP address and port")
	flag.StringVar(&cfg.DataDir, "data-dir", "/data/opt/station-airplay/state", "Persistent identity and pairing directory")
	flag.StringVar(&cfg.Aplay, "aplay", "", "Custom aplay-compatible output (default: bundled station-pcm)")
	flag.StringVar(&cfg.Device, "device", "default", "ALSA playback PCM")
	cfg.OutputDelay = flag.Duration("output-delay", 160*time.Millisecond, "Estimated audio output latency")
	cfg.InitialVolumeDB = flag.Float64("volume", -20, "Initial AirPlay volume in dB")
	check := flag.Bool("check", false, "Check prerequisites without opening listeners or audio")
	showVersion := flag.Bool("version", false, "Print build version")
	flag.Parse()
	if *showVersion {
		fmt.Printf("station-airplay %s (pure Go receiver, bundled PCM output)\n", version)
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, cfg, *check); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg airplay.Config, check bool) error {
	receiver, err := airplay.New(cfg)
	if err != nil {
		return err
	}
	if check {
		result, err := receiver.Check(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("version=%s interface=%s ip=%s aplay=%s pcm=%s state=%s\n",
			version, result.Interface, result.IP, result.Aplay, result.Device, result.DataDir)
		return nil
	}
	if err := receiver.Start(ctx); err != nil {
		return err
	}
	// Wait with an independent context: SIGTERM must complete cleanup before exit.
	return receiver.Wait(context.Background())
}
